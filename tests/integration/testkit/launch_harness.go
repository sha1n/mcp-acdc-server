//go:build unix

package testkit

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"github.com/stretchr/testify/require"
)

// LaunchUnderTest is one stdio launch of the runner in a goroutine, with the
// test playing the host on the other end of its stdin and stdout.
type LaunchUnderTest struct {
	t      testing.TB
	stdin  *io.PipeWriter
	stdout *bufio.Reader
	stderr *lockedBuffer
	done   chan struct{}
	err    error
}

type lineResult struct {
	line []byte
	err  error
}

// Launch starts a launch with args whose spawn seam starts in-process shared
// servers.
func (h *SharedServerHarness) Launch(args ...string) *LaunchUnderTest {
	h.t.Helper()
	return StartLaunch(h.t, h.SessionProxyParams(args), h.Binary, args...)
}

// StartLaunch runs the runner with params and args. It owns the host streams
// of params: both the session proxy streams and the per-session transport
// are wired to the same pipes. When the test ends, host stdin is closed and
// the launch must return within SharedServerDeadline.
func StartLaunch(t testing.TB, params app.RunParams, binary identity.Binary, args ...string) *LaunchUnderTest {
	t.Helper()
	return startLaunch(t, params, binary, nil, args...)
}

func startLaunch(t testing.TB, params app.RunParams, binary identity.Binary, gate *LaunchGate, args ...string) *LaunchUnderTest {
	t.Helper()
	flags := LaunchFlags(t, args)
	hostIn, launchIn := io.Pipe()
	launchOut, hostOut := io.Pipe()
	l := &LaunchUnderTest{
		t:      t,
		stdin:  launchIn,
		stdout: bufio.NewReader(launchOut),
		stderr: &lockedBuffer{},
		done:   make(chan struct{}),
	}
	params.Stdin = hostIn
	params.Stdout = hostOut
	params.CustomIOTransport = &mcp.IOTransport{Reader: hostIn, Writer: hostOut}
	params.Stderr = l.stderr

	gate.join()
	go func() {
		defer close(l.done)
		gate.await()
		l.err = app.RunWithDeps(context.Background(), params, flags, binary)
		// Unblocks host reads and writes still pending on the ended launch.
		hostIn.CloseWithError(io.ErrClosedPipe)
		hostOut.CloseWithError(io.EOF)
	}()
	t.Cleanup(func() {
		// A test that fails before releasing its gate would otherwise leave
		// the launch parked forever and fail the return deadline below.
		gate.Release()
		l.CloseStdin()
		if ended, _ := l.waitWithin(SharedServerDeadline); !ended {
			t.Errorf("launch did not return within %s of host stdin EOF", SharedServerDeadline)
		}
	})
	return l
}

// WriteLine writes one host line; a newline is appended.
func (l *LaunchUnderTest) WriteLine(line string) {
	l.t.Helper()
	_, err := io.WriteString(l.stdin, line+"\n")
	require.NoError(l.t, err)
}

// ReadLine returns the next complete line the launch wrote to host stdout,
// including its newline.
func (l *LaunchUnderTest) ReadLine() []byte {
	l.t.Helper()
	result := make(chan lineResult, 1)
	go func() {
		line, err := l.stdout.ReadBytes('\n')
		result <- lineResult{line: line, err: err}
	}()
	select {
	case r := <-result:
		require.NoError(l.t, r.err)
		return r.line
	case <-time.After(SharedServerDeadline):
		l.t.Fatalf("no host stdout line within %s", SharedServerDeadline)
		return nil
	}
}

// ReadToEOF returns every byte the launch writes to host stdout until the
// launch ends.
func (l *LaunchUnderTest) ReadToEOF() []byte {
	l.t.Helper()
	result := make(chan lineResult, 1)
	go func() {
		rest, err := io.ReadAll(l.stdout)
		result <- lineResult{line: rest, err: err}
	}()
	select {
	case r := <-result:
		require.NoError(l.t, r.err)
		return r.line
	case <-time.After(SharedServerDeadline):
		l.t.Fatalf("host stdout did not end within %s", SharedServerDeadline)
		return nil
	}
}

// Connect initializes an MCP client session over the host streams. Closing
// the session closes host stdin.
func (l *LaunchUnderTest) Connect(ctx context.Context) *mcp.ClientSession {
	l.t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "in-process-host", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: io.NopCloser(l.stdout), Writer: l.stdin}, nil)
	require.NoError(l.t, err)
	l.t.Cleanup(func() {
		if err := session.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			l.t.Logf("client session closed with: %v", err)
		}
	})
	return session
}

// CloseStdin delivers EOF on host stdin.
func (l *LaunchUnderTest) CloseStdin() {
	if err := l.stdin.Close(); err != nil {
		l.t.Errorf("failed to close host stdin: %v", err)
	}
}

// Wait returns the launch result, failing the test if the launch has not
// returned within d.
func (l *LaunchUnderTest) Wait(d time.Duration) error {
	l.t.Helper()
	ended, err := l.waitWithin(d)
	require.True(l.t, ended, "launch did not return within %s", d)
	return err
}

// Ended reports whether the launch has returned.
func (l *LaunchUnderTest) Ended() bool {
	select {
	case <-l.done:
		return true
	default:
		return false
	}
}

// Stderr returns what the launch has written to its stderr so far.
func (l *LaunchUnderTest) Stderr() string { return l.stderr.String() }

func (l *LaunchUnderTest) waitWithin(d time.Duration) (bool, error) {
	select {
	case <-l.done:
		return true, l.err
	case <-time.After(d):
		return false, nil
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
