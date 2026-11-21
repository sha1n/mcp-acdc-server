//go:build unix

package testkit

import (
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A session proxy abandons a spawn it supervises when its session ends, so the
// spawn seam can be called after every launch has returned and the harness
// cleanup has run (#227).
func TestSharedServerHarness_CleanupAwaitsItsCandidatesAndRefusesLaterSpawns(t *testing.T) {
	tb := &deferredCleanupTB{TB: t}
	h := NewSharedServerHarness(tb)
	args := SharedLaunchArgs(CreateTestContentDir(t, nil))
	id := h.Identity(args)
	spawn := h.SpawnSharedServer(args)
	require.NoError(t, spawn(id))

	tb.runCleanups()

	assert.Len(t, h.SharedServerExits(), 1, "the cleanup returned only once its running candidate had ended")
	assert.ErrorIs(t, spawn(id), errHarnessStopped)
	assert.Empty(t, tb.errors())
}

// A session proxy spawns a candidate on every failed dial, so a launch may add
// candidates that lose the lock to the one that builds (#248).
func TestSharedServerHarness_BuiltExitsLeaveOutCandidatesThatLostTheLock(t *testing.T) {
	h := NewSharedServerHarness(t)
	args := SharedLaunchArgs(CreateTestContentDir(t, nil))
	id := h.Identity(args)
	spawn := h.SpawnSharedServer(args)
	release := h.HoldLock(id)
	require.NoError(t, spawn(id))
	h.Eventually(func() bool { return len(h.SharedServerExits()) == 1 }, "the candidate that lost the lock does not exit")
	assert.Empty(t, h.BuiltExits())
	release()

	require.NoError(t, spawn(id))

	h.Eventually(func() bool { return len(h.BuiltExits()) == 1 }, "the built candidate does not linger out")
	assert.Equal(t, []SharedServerExit{{Built: true, Err: nil}}, h.BuiltExits())
	assert.Equal(t, []SharedServerExit{{Built: false, Err: nil}, {Built: true, Err: nil}}, h.SharedServerExits())
}

// deferredCleanupTB runs the cleanups registered on it only when the test
// asks, and records reported errors instead of failing the test.
type deferredCleanupTB struct {
	testing.TB
	mu       sync.Mutex
	cleanups []func()
	reported []string
}

func (tb *deferredCleanupTB) Cleanup(f func()) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.cleanups = append(tb.cleanups, f)
}

func (tb *deferredCleanupTB) Errorf(format string, args ...any) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.reported = append(tb.reported, fmt.Sprintf(format, args...))
}

func (tb *deferredCleanupTB) runCleanups() {
	tb.mu.Lock()
	cleanups := tb.cleanups
	tb.cleanups = nil
	tb.mu.Unlock()
	for _, f := range slices.Backward(cleanups) {
		f()
	}
}

func (tb *deferredCleanupTB) errors() []string {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return slices.Clone(tb.reported)
}

func TestSharedServerHarness_SkipsTheHandlerOfAConnectionWithoutAHello(t *testing.T) {
	h := NewSharedServerHarness(t)
	id := h.Identity(SharedLaunchArgs(CreateTestContentDir(t, nil)))
	var handled atomic.Int32
	h.serveSocket(id, func(net.Conn) { handled.Add(1) })
	socketPath, err := h.Endpoint().SocketPath(id)
	require.NoError(t, err)

	refused, err := net.Dial("unix", socketPath)
	require.NoError(t, err)
	defer func() { _ = refused.Close() }()
	require.NoError(t, refused.(*net.UnixConn).CloseWrite())
	require.NoError(t, refused.SetReadDeadline(time.Now().Add(SharedServerDeadline)))
	received, err := io.ReadAll(refused)
	require.NoError(t, err, "a connection without a hello must be closed at once, not held until cleanup")
	assert.Empty(t, received)
	assert.Zero(t, handled.Load(), "a connection that sent no hello must not reach the handler")

	accepted, err := net.Dial("unix", socketPath)
	require.NoError(t, err)
	defer func() { _ = accepted.Close() }()
	require.NoError(t, SendHello(accepted))
	require.Eventually(t, func() bool { return handled.Load() == 1 }, SharedServerDeadline, 10*time.Millisecond)
}
