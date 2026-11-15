//go:build unix

package sharedserver

import (
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionProxyFallback_DialInTheBacklogOfAClosingListenerReentersDiscoveryAndAttaches(t *testing.T) {
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t))
	var gate *testkit.DiscoveryGate
	h, l := attachWithoutLingerLoss(t, func(h *testkit.SharedServerHarness) *testkit.LaunchUnderTest {
		id := h.Identity(args)
		release := h.HoldLock(id)
		pl := h.ListenWithoutAccepting(id)
		var l *testkit.LaunchUnderTest
		l, gate = testkit.StartHeldLaunch(t, h.SessionProxyParams(args), h.Binary, args...)
		writeHeldRequests(l)

		gate.Open()
		pl.AwaitPendingConnection()
		require.Zero(t, h.Spawns(), "the dial landed in the backlog")
		// The pending connection ends at Shutdown, before release, so the
		// re-entered discovery may spawn a candidate that loses the lock; that
		// candidate exits unbuilt and discovery spawns again.
		pl.Shutdown()
		release()

		requireHeldRequestsAnswered(t, l)
		return l
	})

	assert.Empty(t, l.FallbackWarnings())
	assert.Equal(t, 2, gate.Resolutions(), "the dropped dial re-enters discovery exactly once")
	assert.GreaterOrEqual(t, h.Spawns(), 1)
	assert.Equal(t, 1, h.Builds())
}
