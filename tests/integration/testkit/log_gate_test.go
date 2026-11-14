//go:build unix

package testkit

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// logGate is a FIFO at a log path: a candidate blocks in its write-only open
// of the log until release opens the FIFO for reading. The log is then
// drained until the gate closes, which the test's cleanup always does.
type logGate struct {
	path        string
	releaseOnce sync.Once
	releaseErr  error
	closeOnce   sync.Once
	closeErr    error
	reader      *os.File
	writer      *os.File
	closing     atomic.Bool
	drained     chan struct{}
}

// withholdLog gates the log of id, so a candidate for id blocks opening its
// log, after it takes its lock and before it binds its socket.
func withholdLog(t *testing.T, h *SharedServerHarness, id string) *logGate {
	t.Helper()
	return newLogGate(t, filepath.Join(h.LogDir, id+".log"))
}

func newLogGate(t *testing.T, path string) *logGate {
	t.Helper()
	require.NoError(t, syscall.Mkfifo(path, 0o600))
	g := &logGate{path: path, drained: make(chan struct{})}
	t.Cleanup(func() { assert.NoError(t, g.close()) })
	return g
}

// release lets a pending or later write-only open of the log complete.
func (g *logGate) release() error {
	g.releaseOnce.Do(func() {
		g.releaseErr = g.openAndDrain()
		if g.releaseErr != nil {
			close(g.drained)
		}
	})
	return g.releaseErr
}

func (g *logGate) openAndDrain() error {
	// A non-blocking read-only open returns with or without a writer, where a
	// blocking one could not be cancelled.
	fd, err := syscall.Open(g.path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	// The drain reads blocking: Darwin's kqueue never reports a FIFO
	// readable, so a polled read would stall the candidate once the pipe
	// fills.
	if err := syscall.SetNonblock(fd, false); err != nil {
		return errors.Join(err, syscall.Close(fd))
	}
	reader := os.NewFile(uintptr(fd), g.path)
	// The gate's own writer keeps the drain from reading end-of-file before
	// the candidate opens the log, and wakes the drain to end it.
	writer, err := os.OpenFile(g.path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return errors.Join(err, reader.Close())
	}
	g.reader, g.writer = reader, writer
	go g.drain()
	return nil
}

func (g *logGate) drain() {
	defer close(g.drained)
	buf := make([]byte, 64<<10)
	for !g.closing.Load() {
		if _, err := g.reader.Read(buf); err != nil {
			return
		}
	}
}

// close releases the gate if it is still withheld, ends and joins the drain,
// and removes the FIFO.
func (g *logGate) close() error {
	g.closeOnce.Do(func() {
		var errs []error
		if err := g.release(); err != nil {
			errs = append(errs, err)
		}
		if g.writer != nil {
			g.closing.Store(true)
			// A full pipe refuses the wake byte, but then the drain is not
			// blocked and sees closing after its next read.
			if _, err := g.writer.Write([]byte{0}); err != nil && !errors.Is(err, syscall.EAGAIN) {
				errs = append(errs, err)
			}
		}
		<-g.drained
		if g.reader != nil {
			errs = append(errs, g.reader.Close(), g.writer.Close())
		}
		errs = append(errs, os.Remove(g.path))
		g.closeErr = errors.Join(errs...)
	})
	return g.closeErr
}

// blockedOpening reports whether a goroutine of this process is inside a
// system call of an os.OpenFile that caller made directly. A FIFO's
// write-only open cannot return without a reader, so such a goroutine is
// blocked in that open when the path is a withheld gate.
func blockedOpening(caller string) bool {
	for _, g := range strings.Split(goroutineStacks(), "\n\n") {
		header, frames, _ := strings.Cut(g, "\n")
		if strings.Contains(header, "[syscall") && callsDirectly(frames, caller, "os.OpenFile") {
			return true
		}
	}
	return false
}

func goroutineStacks() string {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return string(buf[:n])
		}
		buf = make([]byte, 2*len(buf))
	}
}

// callsDirectly reports whether the innermost-first frames of one goroutine
// have callee immediately followed by caller.
func callsDirectly(frames, caller, callee string) bool {
	var funcs []string
	for _, line := range strings.Split(frames, "\n") {
		if line == "" || strings.HasPrefix(line, "\t") {
			continue
		}
		name, _, _ := strings.Cut(line, "(")
		funcs = append(funcs, name)
	}
	for i := 0; i+1 < len(funcs); i++ {
		if strings.HasSuffix(funcs[i], callee) && strings.HasSuffix(funcs[i+1], caller) {
			return true
		}
	}
	return false
}

func openForWriting(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	return f.Close()
}

func TestLogGate_ReleaseWithoutAWriterReturnsAndCloseLeavesNothingOpen(t *testing.T) {
	g := newLogGate(t, filepath.Join(t.TempDir(), "gated.log"))

	require.NoError(t, g.release())
	require.NoError(t, g.release())
	require.NoError(t, g.close())
	require.NoError(t, g.close())

	select {
	case <-g.drained:
	default:
		t.Fatal("close returned before the drain ended")
	}
	assert.ErrorIs(t, g.reader.Close(), os.ErrClosed)
	assert.ErrorIs(t, g.writer.Close(), os.ErrClosed)
	assert.NoFileExists(t, g.path)
}

func TestLogGate_CloseUnblocksAWriterThatIsBlockedOpeningAnUnreleasedGate(t *testing.T) {
	g := newLogGate(t, filepath.Join(t.TempDir(), "gated.log"))
	opened := make(chan error, 1)
	go func() { opened <- openForWriting(g.path) }()
	require.Eventually(t, func() bool { return blockedOpening("testkit.openForWriting") }, SharedServerDeadline, pollInterval, "the writer is not blocked opening the gate")

	require.NoError(t, g.close())

	require.Eventually(t, func() bool { return len(opened) == 1 }, SharedServerDeadline, pollInterval, "close did not unblock the writer")
	require.NoError(t, <-opened)
}

func TestLogGate_AbandonedGateLetsABlockedCandidateRunAndStop(t *testing.T) {
	h := NewSharedServerHarness(t)
	args := SharedLaunchArgs(CreateTestContentDir(t, nil))
	id := h.Identity(args)
	g := withholdLog(t, h, id)
	require.NoError(t, h.SpawnSharedServer(args)(id))
	h.Eventually(func() bool { return blockedOpening("sharedserver.openLog") }, "the candidate is not blocked opening its log")

	require.NoError(t, g.close())

	h.Eventually(func() bool { return !blockedOpening("sharedserver.openLog") }, "the candidate is still blocked opening its log")
	// The harness's cleanup fails the test unless the candidate then stops.
}

func TestLogGate_ReleasedGateDrainsMoreThanAPipeBuffers(t *testing.T) {
	g := newLogGate(t, filepath.Join(t.TempDir(), "gated.log"))
	require.NoError(t, g.release())
	log, err := os.OpenFile(g.path, os.O_WRONLY, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = log.Close() })
	written := make(chan error, 1)

	// Pipe buffers hold at most 64 KiB on Linux and macOS.
	go func() {
		_, err := log.Write(make([]byte, 1<<20))
		written <- err
	}()

	require.Eventually(t, func() bool { return len(written) == 1 }, SharedServerDeadline, pollInterval, "the write is not drained")
	require.NoError(t, <-written)
}
