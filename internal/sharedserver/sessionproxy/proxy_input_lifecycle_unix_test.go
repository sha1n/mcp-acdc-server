//go:build unix

package sessionproxy_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noFurtherReadWindow is how long a test watches for a read that must never
// happen; it bounds detection only, never what a correct proxy does.
const noFurtherReadWindow = 300 * time.Millisecond

// scriptedStdin hands one chunk to each Read the test answers and reports
// every Read the session proxy starts.
type scriptedStdin struct {
	reads  chan struct{}
	chunks chan string
}

func newScriptedStdin(t *testing.T) *scriptedStdin {
	stdin := &scriptedStdin{reads: make(chan struct{}, 64), chunks: make(chan string)}
	t.Cleanup(func() { close(stdin.chunks) })
	return stdin
}

func (s *scriptedStdin) Read(p []byte) (int, error) {
	s.reads <- struct{}{}
	chunk, ok := <-s.chunks
	if !ok {
		return 0, io.EOF
	}
	return copy(p, chunk), nil
}

func (s *scriptedStdin) awaitRead(t *testing.T) {
	t.Helper()
	select {
	case <-s.reads:
	case <-time.After(proxyTestDeadline):
		t.Fatal("the session proxy never read host input")
	}
}

func runProxy(t *testing.T, ctx context.Context, cfg sessionproxy.Config) <-chan error {
	t.Helper()
	cfg.Logger = slog.New(slog.DiscardHandler)
	cfg.Stdout = io.Discard
	if cfg.Identity == nil {
		cfg.Identity = func() (string, error) { return proxyTestIdentity, nil }
	}
	cfg.ConnectWait = proxyTestDeadline
	done := make(chan error, 1)
	go func() { done <- sessionproxy.Run(ctx, cfg) }()
	return done
}

func awaitRunResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(proxyTestDeadline):
		t.Fatal("the session proxy did not return")
		return nil
	}
}

func TestRun_StopsConsumingHostInputOnceItReturns(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, stdin *scriptedStdin) error
	}{
		{name: "discovery failure", run: func(t *testing.T, stdin *scriptedStdin) error {
			return awaitRunResult(t, runProxy(t, context.Background(), sessionproxy.Config{
				EndpointDir: proxyTestEndpointDir(t),
				Stdin:       stdin,
				Spawn:       func(string) error { return nil },
				Identity: func() (string, error) {
					stdin.awaitRead(t)
					return "", errors.New("unreadable content")
				},
			}))
		}},
		{name: "cancellation", run: func(t *testing.T, stdin *scriptedStdin) error {
			ctx, cancel := context.WithCancel(context.Background())
			done := runProxy(t, ctx, sessionproxy.Config{
				EndpointDir: proxyTestEndpointDir(t),
				Stdin:       stdin,
				Spawn:       func(string) error { return nil },
			})
			stdin.awaitRead(t)
			cancel()
			return awaitRunResult(t, done)
		}},
		{name: "connection lost after attach", run: func(t *testing.T, stdin *scriptedStdin) error {
			dir := proxyTestEndpointDir(t)
			server := newFakeSharedServer(t, dir)
			done := runProxy(t, context.Background(), sessionproxy.Config{EndpointDir: dir, Stdin: stdin, Spawn: server.spawn})
			stdin.awaitRead(t)
			conn := server.nextConn()
			conn.sendReady()
			require.NoError(t, conn.conn.Close())
			return awaitRunResult(t, done)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdin := newScriptedStdin(t)
			t.Logf("session proxy ended: %v", tt.run(t, stdin))

			stdin.chunks <- "{\"id\":1}\n{\"id\":2}\n"

			select {
			case <-stdin.reads:
				t.Fatal("host input is still consumed after the session proxy returned")
			case <-time.After(noFurtherReadWindow):
			}
		})
	}
}

func TestRun_StdinEOFEndsARelayWhoseSocketWriteIsBlocked(t *testing.T) {
	host, conn := attachedProxy(t)
	host.write(strings.Repeat("x", 8<<20) + "\n")

	ended := time.Now()
	require.NoError(t, host.stdin.Close())

	require.NoError(t, host.result())
	assert.Less(t, time.Since(ended), 500*time.Millisecond)
	require.NoError(t, conn.conn.SetReadDeadline(time.Now().Add(proxyTestDeadline)))
	_, err := io.Copy(io.Discard, conn.conn)
	require.NoError(t, err, "the session proxy closed the connection")
}

func TestRun_AppliesBackPressureToTheHostOnceAttached(t *testing.T) {
	host, _ := attachedProxy(t)
	line := strings.Repeat("x", 1<<20) + "\n"
	written := make(chan error, 1)
	go func() {
		for range 8 {
			if _, err := io.WriteString(host.stdin, line); err != nil {
				written <- err
				return
			}
		}
		written <- nil
	}()

	select {
	case err := <-written:
		t.Fatalf("8 MiB of host input was taken while the shared server read none of it (write result: %v)", err)
	case <-time.After(noFurtherReadWindow):
	}
	host.cancel()
	require.NoError(t, host.result())
	require.NoError(t, host.stdin.Close())
	assert.ErrorIs(t, <-written, io.ErrClosedPipe)
}
