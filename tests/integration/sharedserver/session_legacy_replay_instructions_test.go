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

// The warning is written before the announcements and the held messages, so
// it is on stderr once the later ping has its response.
func TestSessionLegacyReplay_OtherInstructionsWarnOnceAndTheSessionContinues(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	h := testkit.NewSharedServerHarness(t)
	args := recoveryLaunchArgs(newSessionProxyContentDir(t))
	fake := h.ServeLegacyReplay(h.Identity(args),
		testkit.LegacyReplayConnection{Result: legacyResult(testkit.LegacyProtocolVersion, allListChanges, "first"), DropAfter: "tools/list"},
		testkit.LegacyReplayConnection{Result: legacyResult(testkit.LegacyProtocolVersion, allListChanges, "second")})
	l := h.Launch(args...)
	initializeRawLegacy(t, l)

	crashLegacyReplayFake(t, l, 1, deadline)
	l.WriteLine(rawPing(`"after"`))
	readUntilResponse(t, l, `"after"`)

	warnings := stderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": replayMismatchWarning})
	require.Len(t, warnings, 1, "one warning for the recovery: %s", l.Stderr())
	assert.Contains(t, warnings[0]["differs"], "instructions")
	assert.NotContains(t, warnings[0]["differs"], "capabilities")
	assert.False(t, l.Ended(), "the session continues")
	assert.Equal(t, 2, fake.Accepted())
}
