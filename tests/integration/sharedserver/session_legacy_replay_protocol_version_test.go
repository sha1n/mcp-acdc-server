//go:build unix

package sharedserver

import (
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The requests are held while the recovery waits at its endpoint step; the
// unterminated line after them proves the proxy has taken them.
func TestSessionLegacyReplay_OtherProtocolVersionFailsTheRecovery(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	h := testkit.NewSharedServerHarness(t)
	args := recoveryLaunchArgs(newSessionProxyContentDir(t))
	fake := h.ServeLegacyReplay(h.Identity(args),
		testkit.LegacyReplayConnection{Result: legacyResult(testkit.LegacyProtocolVersion, allListChanges, "same"), DropAfter: "tools/list"},
		testkit.LegacyReplayConnection{Result: legacyResult("2025-06-18", allListChanges, "same")})
	l, gate := h.LaunchGatingRecovery(args...)
	initializeRawLegacy(t, l)

	crashLegacyReplayFake(t, l, 1, deadline)
	gate.AwaitHeld()
	l.WriteLine(rawPing("31"))
	l.WriteLine(rawPing(`"s-32"`))
	l.WriteUnterminated("{")
	gate.Release()

	assert.Equal(t, connectionLostError("31")+connectionLostError(`"s-32"`), string(l.ReadToEOF()),
		"every held request gets the connection-lost error in held order, and nothing else is written")
	requireFailedReplay(t, h, l, fake, "protocol_version")
	received := fake.Received()
	require.Len(t, received, 2)
	require.NotEmpty(t, received[1])
	assert.Equal(t, rawLegacyInitialize+"\n", received[1][0], "the replacement received the host's initialize byte for byte")
}
