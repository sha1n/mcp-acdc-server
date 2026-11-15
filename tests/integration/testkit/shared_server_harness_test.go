//go:build unix

package testkit

import (
	"fmt"
	"slices"
	"sync"
	"testing"

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
