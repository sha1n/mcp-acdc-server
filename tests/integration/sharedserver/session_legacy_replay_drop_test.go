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

const replayForwardedLine = `{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info","data":"forwarded during the replay"}}` + "\n"

// Each drop happens at a socket step of the replay (spec 7.3, steps 2 to 4).
// The requests are held while the recovery waits at its endpoint step; the
// unterminated line after them proves the proxy has taken them.
//
// For the notifications/initialized write, the replacement writes a server
// line ahead of the initialize answer and drops right after the answer. The
// host does not read stdout until the drop has happened, and the proxy
// cannot write notifications/initialized before the host has read the
// forwarded line, so that write meets a closed peer.
func TestSessionLegacyReplay_DropDuringTheReplayFailsTheRecovery(t *testing.T) {
	same := legacyResult(testkit.LegacyProtocolVersion, allListChanges, "same")
	cases := []struct {
		name        string
		setLevel    bool
		replacement testkit.LegacyReplayConnection
		forwarded   string
	}{
		{name: "before the initialize response", replacement: testkit.LegacyReplayConnection{Result: same, DropOn: "initialize"}},
		{
			name:        "at notifications/initialized",
			replacement: testkit.LegacyReplayConnection{Result: same, BeforeInitializeAnswer: replayForwardedLine, DropAfter: "initialize"},
			forwarded:   replayForwardedLine,
		},
		{name: "before the setLevel response", setLevel: true, replacement: testkit.LegacyReplayConnection{Result: same, DropOn: "logging/setLevel"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deadline := time.Now().Add(testkit.SharedServerDeadline)
			h := testkit.NewSharedServerHarness(t)
			args := recoveryLaunchArgs(newSessionProxyContentDir(t))
			fake := h.ServeLegacyReplay(h.Identity(args),
				testkit.LegacyReplayConnection{Result: same, DropAfter: "tools/list"}, tc.replacement)
			l, gate := h.LaunchGatingRecovery(args...)
			initializeRawLegacy(t, l)
			if tc.setLevel {
				l.WriteLine(`{"jsonrpc":"2.0","id":"level","method":"logging/setLevel","params":{"level":"debug"}}`)
				requireResponse(t, l, `"level"`)
			}

			crashLegacyReplayFake(t, l, 1, deadline)
			fake.AwaitDropped()
			gate.AwaitHeld()
			l.WriteLine(rawPing("41"))
			l.WriteLine(rawPing(`"s-42"`))
			l.WriteUnterminated("{")
			gate.Release()
			fake.AwaitDropped()

			assert.Equal(t, tc.forwarded+connectionLostError("41")+connectionLostError(`"s-42"`), string(l.ReadToEOF()),
				"every held request gets the connection-lost error in held order, after the lines forwarded during the replay")
			requireFailedReplay(t, h, l, fake, "dropped_in_sequence")
		})
	}
}

// Ruling C on #173: once the last replay step has reached the replacement, a
// drop is first observed by the relay, so it is a new crash that is
// recovered. The second recovery's announcements are read before the second
// crash, because the proxy writes them to the host before it relays.
func TestSessionLegacyReplay_DropAfterTheLastReplayStepIsANewCrashThatRecovers(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	h := testkit.NewSharedServerHarness(t)
	args := recoveryLaunchArgs(newSessionProxyContentDir(t))
	same := legacyResult(testkit.LegacyProtocolVersion, allListChanges, "same")
	fake := h.ServeLegacyReplay(h.Identity(args),
		testkit.LegacyReplayConnection{Result: same, DropAfter: "tools/list"},
		testkit.LegacyReplayConnection{Result: same, DropAfter: "notifications/initialized"},
		testkit.LegacyReplayConnection{Result: same})
	l := h.Launch(args...)
	initializeRawLegacy(t, l)

	crashLegacyReplayFake(t, l, 1, deadline)
	announcements := []string{resourcesListChanged, promptsListChanged}
	assert.Equal(t, announcements, methodsOf(t, []string{string(l.ReadLine()), string(l.ReadLine())}))
	awaitStderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage}, 2, deadline)
	l.WriteLine(rawPing(`"after"`))
	assert.Equal(t, announcements, methodsOf(t, readUntilResponse(t, l, `"after"`)))

	lifecycle := recoveryLifecycleOf(t, l)
	require.GreaterOrEqual(t, len(lifecycle), 6)
	assert.Equal(t, []string{attachMessage, crashWarningMessage, attachMessage, recoveredMessage, crashWarningMessage, attachMessage},
		lifecycle[:6], "the first recovery completed and the drop was one new crash, recovered onto the third connection")
	assert.Len(t, stderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage}), 2)
	assert.False(t, l.Ended())
	received := fake.Received()
	require.Len(t, received, 3)
	assert.Equal(t, []string{rawLegacyInitialize + "\n", rawLegacyInitialized + "\n"}, received[1][:2])
	assert.Zero(t, h.Spawns())
}
