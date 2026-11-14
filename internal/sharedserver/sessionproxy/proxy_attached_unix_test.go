//go:build unix

package sessionproxy_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
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

func TestRun_StdinEOFAfterAttachDeliversEveryLineAlreadyReadInOrder(t *testing.T) {
	host, conn := attachedProxy(t)
	var sent strings.Builder
	for id := 2; id <= 5; id++ {
		line := fmt.Sprintf("{\"id\":%d}\n", id)
		host.write(line)
		sent.WriteString(line)
	}
	// Larger than any socket buffer, so its write is still in progress when
	// stdin ends: the shared server reads nothing until then.
	last := fmt.Sprintf("{\"id\":6,\"pad\":%q}\n", strings.Repeat("x", 1<<20))
	host.write(last)
	sent.WriteString(last)

	require.NoError(t, host.stdin.Close())

	require.NoError(t, conn.conn.SetReadDeadline(time.Now().Add(proxyTestDeadline)))
	received, err := io.ReadAll(conn.lines)
	require.NoError(t, err, "the session proxy closed the connection after the last line")
	assert.True(t, sent.String() == string(received),
		"every line read before EOF reaches the shared server, in order: received %d of %d bytes", len(received), sent.Len())
	require.NoError(t, host.result())
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
