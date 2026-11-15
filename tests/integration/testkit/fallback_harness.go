//go:build unix

package testkit

import (
	"bufio"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
)

const fallbackWarningMessage = `msg="Shared server unavailable, continuing as a per-session server"`

// FallbackWarnings returns the fallback warning lines the launch has written
// to its stderr so far.
func (l *LaunchUnderTest) FallbackWarnings() []string {
	var warnings []string
	for line := range strings.Lines(l.Stderr()) {
		if strings.Contains(line, "level=WARN") && strings.Contains(line, fallbackWarningMessage) {
			warnings = append(warnings, line)
		}
	}
	return warnings
}

// DiscoveryGate holds a session proxy where it resolves its endpoint
// directory. Host input written while the gate is closed is read by the proxy
// before any discovery outcome, so it is certain to be held input.
type DiscoveryGate struct {
	opened      chan struct{}
	once        sync.Once
	resolutions atomic.Int32
}

// StartHeldLaunch is StartLaunch with discovery held by the returned gate.
// The gate opens when the test ends, before host stdin is closed, because a
// held resolution is not interrupted by stdin EOF.
func StartHeldLaunch(t testing.TB, params app.RunParams, binary identity.Binary, args ...string) (*LaunchUnderTest, *DiscoveryGate) {
	t.Helper()
	gate := &DiscoveryGate{opened: make(chan struct{})}
	resolve := params.EndpointDir
	params.EndpointDir = func() (string, error) {
		gate.resolutions.Add(1)
		<-gate.opened
		return resolve()
	}
	l := StartLaunch(t, params, binary, args...)
	t.Cleanup(gate.Open)
	return l, gate
}

// Open lets discovery continue; it is idempotent.
func (g *DiscoveryGate) Open() { g.once.Do(func() { close(g.opened) }) }

// Resolutions counts the endpoint directory resolutions discovery has
// started, so zero means discovery never reached the endpoint step.
func (g *DiscoveryGate) Resolutions() int { return int(g.resolutions.Load()) }

// ConnectionBreak ends a connection before any ready line.
type ConnectionBreak func(conn net.Conn) error

// CloseConnection closes the connection without writing anything. Nothing
// is unread on it, since a session proxy writes nothing before the ready
// line, so the proxy reads end of file on every platform rather than a reset.
func CloseConnection(conn net.Conn) error { return conn.Close() }

// SendInvalidReadyLine writes a complete first line that is not a ready line
// and closes the connection once the session proxy has closed its end.
func SendInvalidReadyLine(conn net.Conn) error {
	if _, err := io.WriteString(conn, "not a ready line\n"); err != nil {
		return errors.Join(err, conn.Close())
	}
	_, err := io.Copy(io.Discard, bufio.NewReader(conn))
	return errors.Join(err, conn.Close())
}

// BreakBeforeReady binds the socket of id as a listener that ends each
// accepted connection with breakConn. After limit connections it closes the
// listener before breaking the last one, which removes the socket, so the
// next dial fails and the session proxy spawns a candidate; a limit of zero
// breaks every connection. accepted counts the connections accepted so far.
// breakConn must return once its connection is closed: when the test ends
// every accepted connection is closed and every breakConn has returned.
func (h *SharedServerHarness) BreakBeforeReady(id string, limit int, breakConn ConnectionBreak) (accepted func() int) {
	h.t.Helper()
	listener := h.listenAt(id)
	var count atomic.Int32
	h.serve(listener, func(conn net.Conn) {
		// The session proxy dials again only after this connection breaks,
		// so the listener is closed before any further dial.
		if n := count.Add(1); limit > 0 && int(n) == limit {
			closeTestListener(h, listener)
		}
		if err := breakConn(conn); err != nil {
			reportUnlessConnectionEnded(h, "failed to break a connection", err)
		}
	})
	return func() int { return int(count.Load()) }
}

func closeTestListener(h *SharedServerHarness, listener net.Listener) {
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		h.t.Errorf("failed to close the test listener: %v", err)
	}
}
