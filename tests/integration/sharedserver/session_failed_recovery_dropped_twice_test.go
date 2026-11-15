//go:build unix

package sharedserver

import (
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
)

// The replacement is bound only after the old shared server exited and
// while the recovery is held before its dial, so every dial of the recovery
// reaches it; it never sends a ready line, so no outage line is relayed.
func TestSessionFailedRecovery_ReplacementDroppingTwiceBeforeTheReadyLineFails(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	h := testkit.NewSharedServerHarness(t)
	l, gate := h.LaunchGatingRecovery(recoveryLaunchArgs(newSessionProxyContentDir(t))...)
	attachRecords(t, l, 1, deadline)
	startupSpawns := h.Spawns()
	id := l.SharedServerIdentity()

	crashIntoHeldRecovery(t, h, l, gate, deadline)
	h.RemoveSocket(id)
	accepted := h.BreakBeforeReady(id, 0, testkit.CloseConnection)
	l.WriteLine(modernSearch("21", "outage-first"))
	l.WriteLine(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":21}}`)
	l.WriteLine(modernSearch(`"s-22"`, "outage-second"))
	l.WriteUnterminated("{")
	gate.Release()

	assert.Equal(t, connectionLostError("21")+connectionLostError(`"s-22"`), string(l.ReadToEOF()),
		"every held request gets the connection-lost error in held order, and nothing else is written")
	requireFailedRecovery(t, h, l, "dropped_twice")
	assert.Equal(t, 2, accepted(), "the recovery re-entered exactly once")
	assert.Equal(t, startupSpawns, h.Spawns(), "nothing was spawned at recovery")
	assert.Equal(t, 1, h.Builds())
}
