//go:build unix

package testkit

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/stretchr/testify/require"
)

// RecoveryGate holds every endpoint directory resolution of a session proxy
// after its first connect until Release. A recovery resolves the endpoint
// directory after it computed the identity and before its connect wait
// starts, so while a resolution is held the proxy is detached from host input
// and no connect wait runs.
type RecoveryGate struct {
	h           *SharedServerHarness
	resolutions atomic.Int32
	held        chan struct{}
	released    chan struct{}
	release     sync.Once
}

// LaunchGatingRecovery is Launch with every endpoint directory resolution
// after the first held by the returned gate. The gate is released when the
// test ends.
func (h *SharedServerHarness) LaunchGatingRecovery(args ...string) (*LaunchUnderTest, *RecoveryGate) {
	h.t.Helper()
	g := &RecoveryGate{h: h, held: make(chan struct{}, heldSearchBacklog), released: make(chan struct{})}
	params := h.SessionProxyParams(args)
	resolve := params.EndpointDir
	params.EndpointDir = func() (string, error) {
		if g.resolutions.Add(1) > 1 {
			select {
			case g.held <- struct{}{}:
			case <-g.released:
			}
			<-g.released
		}
		return resolve()
	}
	l := StartLaunch(h.t, params, h.Binary, args...)
	h.t.Cleanup(g.Release)
	h.addLaunch(l)
	return l, g
}

// AwaitHeld returns once one more resolution than earlier calls returned for
// is held, failing the test if none is within SharedServerDeadline.
func (g *RecoveryGate) AwaitHeld() {
	g.h.t.Helper()
	select {
	case <-g.held:
	case <-time.After(SharedServerDeadline):
		g.h.t.Fatalf("no endpoint directory resolution was held within %s", SharedServerDeadline)
	}
}

// Release lets every held and later resolution proceed, and AwaitHeld no
// longer sees later ones; it is idempotent.
func (g *RecoveryGate) Release() { g.release.Do(func() { close(g.released) }) }

// Resolutions counts the endpoint directory resolutions started so far,
// including the first, ungated one.
func (g *RecoveryGate) Resolutions() int { return int(g.resolutions.Load()) }

// RemoveSocket removes the socket file of id, which a shared server that has
// exited leaves behind because only a lock holder may unlink it, so the test
// can bind the path itself.
func (h *SharedServerHarness) RemoveSocket(id string) {
	h.t.Helper()
	socketPath, err := h.Endpoint().SocketPath(id)
	require.NoError(h.t, err)
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		require.NoError(h.t, err)
	}
}

// ServeReadyThenDrop binds the socket of id as a replacement shared server
// that accepts one connection, sends it a ready line and then ends it with
// drop, so the session proxy attaches and then loses the connection. The
// listener is closed, which removes the socket, before the ready line is
// sent, so the recovery that follows the drop does not reach it again.
// accepted counts the connections accepted so far. drop must return once its
// connection is closed.
func (h *SharedServerHarness) ServeReadyThenDrop(id string, drop ConnectionBreak) (accepted func() int) {
	h.t.Helper()
	listener := h.listenAt(id)
	var count atomic.Int32
	h.serve(listener, func(conn net.Conn) {
		count.Add(1)
		closeTestListener(h, listener)
		readyLine, err := sharedserver.EncodeReadyLine(sharedserver.NewSessionID(), "/var/log/acdc-test.log")
		if err != nil {
			h.t.Errorf("failed to encode the ready line: %v", err)
			return
		}
		if _, err := conn.Write(readyLine); err != nil {
			reportUnlessConnectionEnded(h, "failed to write the ready line", err)
			return
		}
		if err := drop(conn); err != nil {
			reportUnlessConnectionEnded(h, "failed to drop the connection", err)
		}
	})
	return func() int { return int(count.Load()) }
}
