//go:build unix

package sharedserver

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const refusedHelloLog = "Refused shared server connection without a valid hello line"

type helloAcceptor struct {
	socketPath  string
	logs        *syncLogBuffer
	revalidated atomic.Int32
}

func startHelloAcceptor(t *testing.T, helloTimeout time.Duration) *helloAcceptor {
	t.Helper()
	listener, socketPath := bindTestEndpoint(t)
	ep, err := endpoint.OpenEndpoint(filepath.Dir(socketPath))
	require.NoError(t, err)
	h := &helloAcceptor{socketPath: socketPath, logs: &syncLogBuffer{}}
	ctx, cancel := context.WithCancel(context.Background())
	acceptor := newSessionAcceptor(ctx, slog.New(newSessionHandler(slog.NewTextHandler(h.logs, nil))), listener, ep, testIdentity, time.Hour)
	acceptor.helloTimeout = helloTimeout
	acceptor.start()
	done := make(chan error, 1)
	go func() {
		done <- acceptor.serve(mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil), func(context.Context) { h.revalidated.Add(1) }, "")
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(serveTestDeadline):
			t.Error("acceptor did not stop")
		}
	})
	return h
}

// awaitRefusal asserts that the connection ends without a ready line and
// that the refusal is logged without a session ever being admitted. The
// connection may end with a reset instead of EOF: Linux resets a Unix socket
// closed with unread bytes, as after an oversized hello.
func (h *helloAcceptor) awaitRefusal(t *testing.T, reader *bufio.Reader) {
	t.Helper()
	line, err := reader.ReadBytes('\n')
	assert.True(t, errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET),
		"a refused connection must close without a ready line, got %v", err)
	assert.Empty(t, line, "a refused connection must not get a ready line")
	assert.Eventually(t, func() bool { return strings.Contains(h.logs.String(), refusedHelloLog) }, serveTestDeadline, 10*time.Millisecond)
	assert.NotContains(t, h.logs.String(), "Session attached")
	assert.Zero(t, h.revalidated.Load(), "a refused connection must not be admitted")
}

// sendTestHello writes the session hello a session proxy sends first.
func sendTestHello(t *testing.T, conn net.Conn) {
	t.Helper()
	hello, err := EncodeHelloLine(HelloSession)
	require.NoError(t, err)
	_, err = conn.Write(hello)
	require.NoError(t, err)
}

func TestSessionAcceptor_ReadsTheHelloBeforeWritingTheReadyLine(t *testing.T) {
	h := startHelloAcceptor(t, serveTestDeadline)
	conn, reader := dialRawAcceptorTestSocket(t, h.socketPath)

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(200*time.Millisecond)))
	_, err := reader.ReadBytes('\n')
	var netErr net.Error
	require.ErrorAs(t, err, &netErr, "no ready line may arrive before the hello")
	require.True(t, netErr.Timeout())

	sendTestHello(t, conn)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(serveTestDeadline)))
	line, err := reader.ReadBytes('\n')
	require.NoError(t, err)
	_, err = DecodeReadyLine(line)
	require.NoError(t, err)
	assert.EqualValues(t, 1, h.revalidated.Load())
}

func TestSessionAcceptor_RefusesAConnectionWithoutAValidHello(t *testing.T) {
	tests := []struct {
		name         string
		helloTimeout time.Duration
		send         func(t *testing.T, conn net.Conn)
		reason       string
	}{
		{
			name:         "missing",
			helloTimeout: serveTestDeadline,
			send: func(t *testing.T, conn net.Conn) {
				require.NoError(t, conn.(*net.UnixConn).CloseWrite())
			},
			reason: "EOF",
		},
		{
			name:         "malformed",
			helloTimeout: serveTestDeadline,
			send: func(t *testing.T, conn net.Conn) {
				_, err := io.WriteString(conn, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`+"\n")
				require.NoError(t, err)
			},
			reason: "not a hello line",
		},
		{
			name:         "too long",
			helloTimeout: serveTestDeadline,
			send: func(t *testing.T, conn net.Conn) {
				_, err := io.WriteString(conn, strings.Repeat("x", 2*maxHelloLineLength))
				require.NoError(t, err)
			},
			reason: "line too long",
		},
		{
			name:         "late",
			helloTimeout: 50 * time.Millisecond,
			send:         func(*testing.T, net.Conn) {},
			reason:       "timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := startHelloAcceptor(t, tt.helloTimeout)
			conn, reader := dialRawAcceptorTestSocket(t, h.socketPath)

			tt.send(t, conn)

			h.awaitRefusal(t, reader)
			assert.Contains(t, h.logs.String(), tt.reason)
		})
	}
}
