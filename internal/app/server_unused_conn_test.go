package app

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	// lateFirstRequestDelay is how long after shutdown begins a pre-dialed
	// connection sends its first request; it is well inside the short window
	// unused connections get once shutdown begins.
	lateFirstRequestDelay = 100 * time.Millisecond
	// unusedConnShutdownBound is the most a shutdown may take when only an
	// unused connection is open: the short unused-connection window plus
	// overhead, well under DefaultShutdownGrace and the five seconds
	// http.Server.Shutdown would otherwise wait (golang/go#22682).
	unusedConnShutdownBound = 2 * time.Second
)

func TestServeSSEServer_ServesFirstRequestArrivingShortlyAfterShutdownBegins(t *testing.T) {
	var served atomic.Bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/late" {
			served.Store(true)
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := newServeTestServer(t, handler)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ServeSSEServer(ctx, slog.New(slog.DiscardHandler), srv.server) }()
	srv.awaitReady(t, done)

	conn := srv.dialAccepted(t)

	cancel()
	time.Sleep(lateFirstRequestDelay)
	_, err := io.WriteString(conn, "GET /late HTTP/1.1\r\nHost: test\r\nConnection: close\r\n\r\n")
	require.NoError(t, err, "the pre-dialed connection was closed before its first request was written")
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(readyTimeout)))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err, "a first request sent shortly after shutdown began got no response")
	_ = resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, served.Load(), "the handler did not run for the late first request")
	require.NoError(t, awaitSSEReturn(t, done, unusedConnShutdownBound))
}

func TestServeSSEServer_UnusedConnectionDoesNotHoldShutdownForTheGrace(t *testing.T) {
	srv := newServeTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ServeSSEServer(ctx, slog.New(slog.DiscardHandler), srv.server) }()
	srv.awaitReady(t, done)
	conn := srv.dialAccepted(t)

	started := time.Now()
	cancel()

	require.NoError(t, awaitSSEReturn(t, done, unusedConnShutdownBound))
	require.Less(t, time.Since(started), DefaultShutdownGrace, "the unused connection held shutdown for the grace")
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(promptShutdown)))
	_, err := conn.Read(make([]byte, 1))
	require.Error(t, err, "the unused connection was not closed by the shutdown")
	var netErr net.Error
	if errors.As(err, &netErr) {
		require.False(t, netErr.Timeout(), "the unused connection was still open after shutdown")
	}
}

type serveTestServer struct {
	server   *http.Server
	addr     string
	accepted chan struct{}
}

// newServeTestServer returns a fresh server on a free port whose accepted
// connections are reported on accepted. ConnContext is used because
// ServeSSEServer owns ConnState.
func newServeTestServer(t *testing.T, handler http.Handler) *serveTestServer {
	t.Helper()
	settings := sseTestSettings(t)
	s := &serveTestServer{addr: sseURL(settings, ""), accepted: make(chan struct{}, 16)}
	s.server = &http.Server{
		Addr:              net.JoinHostPort(settings.Host, strconv.Itoa(settings.Port)),
		Handler:           handler,
		ReadHeaderTimeout: readyTimeout,
		ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
			select {
			case s.accepted <- struct{}{}:
			default:
			}
			return ctx
		},
	}
	return s
}

func (s *serveTestServer) awaitReady(t *testing.T, done <-chan error) {
	t.Helper()
	client := sseTestClient(t)
	client.Timeout = time.Second
	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			require.FailNowf(t, "server returned before it was ready", "error: %v", err)
		default:
		}
		resp, err := client.Get(s.addr + "/ready")
		if err == nil {
			_ = resp.Body.Close()
			client.CloseIdleConnections()
			s.drainAccepted()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.FailNow(t, "server never answered")
}

func (s *serveTestServer) drainAccepted() {
	for {
		select {
		case <-s.accepted:
		default:
			return
		}
	}
}

// dialAccepted opens a raw connection and waits until the server accepted it,
// without sending a request on it.
func (s *serveTestServer) dialAccepted(t *testing.T) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", s.server.Addr, readyTimeout)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	select {
	case <-s.accepted:
	case <-time.After(readyTimeout):
		require.FailNow(t, "the server never accepted the connection")
	}
	return conn
}
