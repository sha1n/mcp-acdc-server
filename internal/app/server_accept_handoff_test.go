package app

import (
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

// The accept-handoff test requires this production seam in package app:
//
//	func withAfterInnerAccept(hook func(net.Conn)) SSEOption
//
// It stores hook in sseOptions; ServeSSEServer hands it to its
// stoppableListener, whose Accept calls it with the accepted connection on
// the accepting goroutine right after the inner listener's Accept succeeds
// and before onAccept publishes the connection. A nil hook is not called.
// Scoping it to one ServeSSEServer call keeps it from leaking across tests.

// awaitListenerClosed blocks until a fresh dial to addr is refused, which
// shows that shutdown has closed the inner listener. It does not depend on
// stopAccepting having returned.
func awaitListenerClosed(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(5 * time.Millisecond)
	}
	require.FailNow(t, "shutdown never closed the listener")
}

func TestServeSSEServer_ServesConnectionAcceptedButNotYetPublishedWhenShutdownBegins(t *testing.T) {
	var served atomic.Bool
	srv := newServeTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/handoff" {
			served.Store(true)
		}
		w.WriteHeader(http.StatusOK)
	}))
	var armed atomic.Bool
	held := make(chan struct{}, 1)
	release := make(chan struct{})
	var closeRelease sync.Once
	releaseAccept := func() { closeRelease.Do(func() { close(release) }) }
	t.Cleanup(releaseAccept)
	hook := withAfterInnerAccept(func(net.Conn) {
		if armed.CompareAndSwap(true, false) {
			held <- struct{}{}
			<-release
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ServeSSEServer(ctx, slog.New(slog.DiscardHandler), srv.server, hook) }()
	srv.awaitReady(t, done)

	armed.Store(true)
	conn, err := net.DialTimeout("tcp", srv.server.Addr, readyTimeout)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	select {
	case <-held:
	case <-time.After(readyTimeout):
		require.FailNow(t, "the inner listener never accepted the connection")
	}
	_, err = io.WriteString(conn, "GET /handoff HTTP/1.1\r\nHost: test\r\nConnection: close\r\n\r\n")
	require.NoError(t, err)

	cancel()
	awaitListenerClosed(t, srv.server.Addr)
	releaseAccept()

	resp := readHandoffResponse(t, conn, "a connection accepted before shutdown but published after the window check was closed without serving its buffered first request")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, served.Load(), "the handler did not run for the first request")
	require.NoError(t, awaitSSEReturn(t, done, unusedConnShutdownBound))
}
