//go:build unix

package sessionproxy_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sessionproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func attachedProxy(t *testing.T) (*proxyHost, *fakeSharedConn) {
	t.Helper()
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	host := startProxy(t, sessionproxy.Config{EndpointDir: dir, Spawn: server.spawn})
	conn := server.nextConn()
	conn.sendReady()
	host.write("{\"id\":1}\n")
	require.Equal(t, "{\"id\":1}\n", conn.readLine(), "the session is relaying")
	return host, conn
}

func TestRun_StdinEOFOnARelayingSessionClosesTheConnection(t *testing.T) {
	host, conn := attachedProxy(t)

	require.NoError(t, host.stdin.Close())

	require.NoError(t, host.result())
	conn.requireClosedByProxy()
}

func TestRun_CancellationOfARelayingSessionEndsItWellInside500ms(t *testing.T) {
	host, conn := attachedProxy(t)

	cancelled := time.Now()
	host.cancel()

	require.NoError(t, host.result())
	assert.Less(t, time.Since(cancelled), 500*time.Millisecond)
	conn.requireClosedByProxy()
}

type brokenHostOutput struct{ err error }

func (o brokenHostOutput) Write([]byte) (int, error) { return 0, o.err }

func TestRun_FailsWhenTheHostOutputBreaks(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	stdin, hostWriter := io.Pipe()
	t.Cleanup(func() { _ = hostWriter.Close() })
	outputErr := errors.New("host went away")
	done := make(chan error, 1)
	go func() {
		done <- sessionproxy.Run(context.Background(), sessionproxy.Config{
			EndpointDir: dir,
			Identity:    func() (string, error) { return proxyTestIdentity, nil },
			ConnectWait: proxyTestDeadline,
			Spawn:       server.spawn,
			Logger:      slog.New(slog.DiscardHandler),
			Stdin:       stdin,
			Stdout:      brokenHostOutput{err: outputErr},
		})
	}()
	conn := server.nextConn()
	conn.sendReady()

	conn.write("{\"id\":1,\"result\":{}}\n")

	select {
	case err := <-done:
		require.ErrorIs(t, err, outputErr)
	case <-time.After(proxyTestDeadline):
		t.Fatal("the session proxy did not return")
	}
	conn.requireClosedByProxy()
}
