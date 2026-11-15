//go:build unix

package sessionproxy_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rejectingHost struct{ err error }

func (w rejectingHost) Write([]byte) (int, error) { return 0, w.err }

func TestRun_FailedWriteToTheHostEndsTheSessionWithoutRecovering(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	stdin, hostWriter := io.Pipe()
	t.Cleanup(func() { _ = hostWriter.Close() })
	hostErr := errors.New("host stdout closed")
	done := make(chan error, 1)
	go func() {
		done <- sessionproxy.Run(context.Background(), sessionproxy.Config{
			EndpointDir: fixedEndpointDir(dir),
			Identity:    func() (string, error) { return proxyTestIdentity, nil },
			ConnectWait: proxyTestDeadline,
			Spawn:       server.spawn,
			Logger:      slog.New(slog.DiscardHandler),
			Fallback:    unexpectedFallback(t),
			Stdin:       stdin,
			Stdout:      rejectingHost{err: hostErr},
		})
	}()
	conn := server.nextConn()

	conn.write(readyLine(t) + "{\"id\":1,\"result\":{}}\n")

	err := awaitRunResult(t, done)
	require.ErrorIs(t, err, hostErr)
	assert.NotContains(t, err.Error(), "recover", "a host write failure is not a crash")
}
