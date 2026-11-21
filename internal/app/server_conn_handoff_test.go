package app

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// handoffHold is how long a gated connection is held in a handoff once
// shutdown has begun without its handler starting; it is well inside
// unusedConnWindow, so a server that waits for the handoff still serves the
// request within the window.
const handoffHold = unusedConnWindow / 4

// The handler-entry test requires this production seam in package app:
//
//	var afterConnActive func()
//
// When non-nil, the ConnState tracker installed by ServeSSEServer calls it on
// the connection's goroutine right after it records http.StateActive for that
// connection, outside any lock, before net/http checks its shutdown flag and
// runs the handler.
func setAfterConnActive(t *testing.T, hook func()) {
	t.Helper()
	previous := afterConnActive
	afterConnActive = hook
	t.Cleanup(func() { afterConnActive = previous })
}

// shutdownBegan returns a channel closed once srv.Shutdown has begun, after
// which net/http drops any request whose handler has not started yet.
func shutdownBegan(srv *http.Server) <-chan struct{} {
	began := make(chan struct{})
	var once sync.Once
	srv.RegisterOnShutdown(func() { once.Do(func() { close(began) }) })
	return began
}

// holdUntilShutdownOrHandoff blocks until Shutdown has begun or handoffHold
// has passed, whichever is first.
func holdUntilShutdownOrHandoff(began <-chan struct{}) {
	select {
	case <-began:
	case <-time.After(handoffHold):
	}
}

func TestServeSSEServer_ServesFirstRequestWhoseHandlerHasNotStartedWhenShutdownBegins(t *testing.T) {
	var served atomic.Bool
	srv := newServeTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/handoff" {
			served.Store(true)
		}
		w.WriteHeader(http.StatusOK)
	}))
	began := shutdownBegan(srv.server)
	var armed atomic.Bool
	held := make(chan struct{}, 1)
	setAfterConnActive(t, func() {
		if !armed.CompareAndSwap(true, false) {
			return
		}
		held <- struct{}{}
		holdUntilShutdownOrHandoff(began)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ServeSSEServer(ctx, slog.New(slog.DiscardHandler), srv.server) }()
	srv.awaitReady(t, done)
	conn := srv.dialAccepted(t)

	armed.Store(true)
	cancel()
	_, err := io.WriteString(conn, "GET /handoff HTTP/1.1\r\nHost: test\r\nConnection: close\r\n\r\n")
	require.NoError(t, err, "the last pending connection was closed before its first request was written")
	select {
	case <-held:
	case <-time.After(readyTimeout):
		require.FailNow(t, "the connection never turned active")
	}

	resp := readHandoffResponse(t, conn, "a first request whose headers completed inside the window was dropped before its handler started")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, served.Load(), "the handler did not run for the first request")
	require.NoError(t, awaitSSEReturn(t, done, unusedConnShutdownBound))
}

// The caller's ConnContext runs after Accept returns and before net/http
// registers the connection as http.StateNew, so it gates that handoff;
// ServeSSEServer must keep calling the ConnContext it was given.
func TestServeSSEServer_ServesConnectionAcceptedButNotYetRegisteredWhenShutdownBegins(t *testing.T) {
	var served atomic.Bool
	srv := newServeTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/handoff" {
			served.Store(true)
		}
		w.WriteHeader(http.StatusOK)
	}))
	began := shutdownBegan(srv.server)
	var armed atomic.Bool
	held := make(chan struct{}, 1)
	cancelled := make(chan struct{})
	var closeCancelled sync.Once
	releaseCancelled := func() { closeCancelled.Do(func() { close(cancelled) }) }
	t.Cleanup(releaseCancelled)
	connContext := srv.server.ConnContext
	srv.server.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		if armed.CompareAndSwap(true, false) {
			held <- struct{}{}
			<-cancelled
			holdUntilShutdownOrHandoff(began)
		}
		return connContext(ctx, c)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ServeSSEServer(ctx, slog.New(slog.DiscardHandler), srv.server) }()
	srv.awaitReady(t, done)

	armed.Store(true)
	conn, err := net.DialTimeout("tcp", srv.server.Addr, readyTimeout)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	select {
	case <-held:
	case <-time.After(readyTimeout):
		require.FailNow(t, "the server never accepted the connection")
	}
	cancel()
	releaseCancelled()
	_, err = io.WriteString(conn, "GET /handoff HTTP/1.1\r\nHost: test\r\nConnection: close\r\n\r\n")
	require.NoError(t, err, "the accepted connection was closed before its first request was written")

	resp := readHandoffResponse(t, conn, "a connection accepted before shutdown but registered after the window check was closed without serving its first request")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, served.Load(), "the handler did not run for the first request")
	require.NoError(t, awaitSSEReturn(t, done, unusedConnShutdownBound))
}

func readHandoffResponse(t *testing.T, conn net.Conn, failure string) *http.Response {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(readyTimeout)))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err, failure)
	_ = resp.Body.Close()
	return resp
}
