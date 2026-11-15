//go:build unix

package sharedserver

import (
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionFailedRecovery_ReplacementDroppingAfterTheReadyLineIsANewCrashThatRecovers(t *testing.T) {
	requireReadyThenDropRecovers(t, testkit.CloseConnection)
}

// requireReadyThenDropRecovers recovers a crash onto a replacement that sends
// its ready line and then ends the connection with drop. The replacement is
// bound while the recovery is held before its dial, and it removes its
// socket before the ready line, so the second recovery spawns a real shared
// server. The old shared server has exited, so that one wins the lock.
func requireReadyThenDropRecovers(t *testing.T, drop testkit.ConnectionBreak) {
	t.Helper()
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	h := testkit.NewSharedServerHarness(t)
	l, gate := h.LaunchGatingRecovery(recoveryLaunchArgs(newSessionProxyContentDir(t))...)
	attachRecords(t, l, 1, deadline)
	id := l.SharedServerIdentity()

	crashIntoHeldRecovery(t, h, l, gate, deadline)
	h.RemoveSocket(id)
	accepted := h.ServeReadyThenDrop(id, drop)
	gate.Release()

	awaitStderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage}, 2, deadline)
	attaches := attachRecords(t, l, 3, deadline)
	requireModernSearchSucceeds(t, l, "41")

	assert.Equal(t, "/var/log/acdc-test.log", attaches[1]["log"], "the second attach is to the replacement")
	assert.Equal(t, attaches[0]["log"], attaches[2]["log"], "the third attach is to a real shared server")
	require.Len(t, stderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage}), 2,
		"the drop after the ready line is one new crash")
	assert.Equal(t, 1, accepted())
	assert.Equal(t, 2, h.Builds(), "the second recovery built a new shared server")
	assert.Zero(t, h.FallbackWarnings())
}
