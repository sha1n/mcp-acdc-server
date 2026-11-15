//go:build unix

package sharedserver

import (
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eofEndBound is far below the configured connect wait, so a launch that
// waited for its expiry would fail the bound.
const eofEndBound = 5 * time.Second

// The test holds the lock before releasing the recovery, so no candidate can
// win it; a spawn observed after the release proves the recovery is inside
// its connect wait when host stdin ends.
func TestSessionFailedRecovery_StdinEOFDuringTheOutageEndsTheRunnerWithoutWaitingForTheConnectWait(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	h := testkit.NewSharedServerHarness(t)
	l, gate := h.LaunchGatingRecovery(recoveryLaunchArgs(newSessionProxyContentDir(t), "--shared-server-connect-wait", "30s")...)
	attachRecords(t, l, 1, deadline)

	crashIntoHeldRecovery(t, h, l, gate, deadline)
	h.HoldLock(l.SharedServerIdentity())
	spawned := h.Spawns()
	gate.Release()
	h.Eventually(func() bool { return h.Spawns() > spawned }, "the recovery spawned a candidate inside its connect wait")

	l.CloseStdin()
	require.NoError(t, l.Wait(eofEndBound), "host stdin EOF ends the session normally")
	assert.Empty(t, string(l.ReadToEOF()))
	assert.Zero(t, h.FallbackWarnings())
	assert.Equal(t, 1, h.Builds())
}
