//go:build unix

package sessionproxy_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// heldHostOutput blocks its first write until released, as a host that is
// slow to read, and passes every later write on to lines.
type heldHostOutput struct {
	entered chan struct{}
	release chan error
	once    sync.Once
	lines   chan string
}

func newHeldHostOutput(t *testing.T) *heldHostOutput {
	o := &heldHostOutput{entered: make(chan struct{}), release: make(chan error, 1), lines: make(chan string, 16)}
	t.Cleanup(func() {
		select {
		case o.release <- nil:
		default:
		}
	})
	return o
}

func (o *heldHostOutput) Write(p []byte) (int, error) {
	first := false
	o.once.Do(func() { first = true })
	if !first {
		o.lines <- string(p)
		return len(p), nil
	}
	close(o.entered)
	if err := <-o.release; err != nil {
		return 0, err
	}
	return len(p), nil
}

func (o *heldHostOutput) awaitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-o.entered:
	case <-time.After(proxyTestDeadline):
		t.Fatal("the session proxy never wrote to the host")
	}
}

type writeFirstCrash struct {
	server *fakeSharedServer
	old    *fakeSharedConn
	stdin  *io.PipeWriter
	output *heldHostOutput
	logs   *logRecorder
	cancel context.CancelFunc
	done   chan error
}

// crashDetectedByAWrite makes the forwarder see the crash first: the reader
// is blocked handing a server line to the host, so only the write of a host
// line to the closed connection can notice that the server went away.
func crashDetectedByAWrite(t *testing.T) *writeFirstCrash {
	t.Helper()
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	stdin, hostWriter := io.Pipe()
	t.Cleanup(func() { _ = hostWriter.Close() })
	output := newHeldHostOutput(t)
	logs := &logRecorder{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c := &writeFirstCrash{server: server, stdin: hostWriter, output: output, logs: logs, cancel: cancel, done: make(chan error, 1)}
	go func() {
		c.done <- sessionproxy.Run(ctx, sessionproxy.Config{
			EndpointDir: fixedEndpointDir(dir),
			Identity:    func() (string, error) { return proxyTestIdentity, nil },
			ConnectWait: proxyTestDeadline,
			Spawn:       server.spawn,
			Logger:      slog.New(logs),
			Fallback:    unexpectedFallback(t),
			Stdin:       stdin,
			Stdout:      output,
		})
	}()
	c.old = server.nextConn()
	c.old.sendReadyAs("first")
	c.old.write(`{"jsonrpc":"2.0","method":"notifications/progress"}` + "\n")
	output.awaitEntered(t)
	c.old.crash()
	_, err := io.WriteString(hostWriter, request("1"))
	require.NoError(t, err)
	return c
}

func (c *writeFirstCrash) result(t *testing.T) error {
	t.Helper()
	select {
	case err := <-c.done:
		return err
	case <-time.After(proxyTestDeadline):
		t.Fatal("the session proxy did not return")
		return nil
	}
}

func TestRun_ACrashSeenByAWriteRecoversAndSendsThatLineToTheNewServer(t *testing.T) {
	c := crashDetectedByAWrite(t)

	c.output.release <- nil

	recovered := c.server.nextConn()
	recovered.sendReadyAs("second")
	assert.Equal(t, request("1"), recovered.readLine(), "a line not fully written is not in flight")
	recovered.write(response("1"))
	select {
	case line := <-c.output.lines:
		assert.Equal(t, response("1"), line)
	case <-time.After(proxyTestDeadline):
		t.Fatal("the response never reached the host")
	}
	warnings := c.logs.matching(slog.LevelWarn, crashWarning)
	require.Len(t, warnings, 1)
	assert.Equal(t, "0", warnings[0].attrs["in_flight"])
}

func TestRun_StdinEOFWhileACrashWaitsForTheHostEndsTheSession(t *testing.T) {
	c := crashDetectedByAWrite(t)

	require.NoError(t, c.stdin.Close())

	require.NoError(t, c.result(t))
	assert.Empty(t, c.logs.matching(slog.LevelWarn, crashWarning))
}

func TestRun_CancellationWhileACrashWaitsForTheHostEndsTheSession(t *testing.T) {
	c := crashDetectedByAWrite(t)

	c.cancel()

	require.NoError(t, c.result(t))
	assert.Empty(t, c.logs.matching(slog.LevelWarn, crashWarning))
}

func TestRun_FailsWhenTheHostOutputBreaksWhileACrashWaitsForIt(t *testing.T) {
	c := crashDetectedByAWrite(t)
	outputErr := errors.New("host went away")

	c.output.release <- outputErr

	require.ErrorIs(t, c.result(t), outputErr)
	assert.Empty(t, c.logs.matching(slog.LevelWarn, crashWarning))
}
