//go:build unix

package sharedserver

import (
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
)

// A real shared server always advertises the same capabilities, so a fake
// first server scripts each subset. The ping is sent once the crash warning
// is on stderr: the announcements precede its response, so a missing one is
// proven missing by the response arriving without it.
func TestSessionLegacyReplay_AnnouncesOnlyTheListChangesTheHostSawAdvertised(t *testing.T) {
	cases := []struct {
		name         string
		capabilities string
		want         []string
	}{
		{name: "resources and prompts", capabilities: allListChanges, want: []string{resourcesListChanged, promptsListChanged}},
		{name: "resources only", capabilities: `{"tools":{"listChanged":true},"resources":{"listChanged":true},"prompts":{}}`, want: []string{resourcesListChanged}},
		{name: "prompts only", capabilities: `{"tools":{"listChanged":true},"resources":{"subscribe":true},"prompts":{"listChanged":true}}`, want: []string{promptsListChanged}},
		{name: "neither", capabilities: `{"tools":{"listChanged":true},"resources":{"listChanged":false}}`, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deadline := time.Now().Add(testkit.SharedServerDeadline)
			h := testkit.NewSharedServerHarness(t)
			args := recoveryLaunchArgs(newSessionProxyContentDir(t))
			result := legacyResult(testkit.LegacyProtocolVersion, tc.capabilities, "legacy replay")
			fake := h.ServeLegacyReplay(h.Identity(args),
				testkit.LegacyReplayConnection{Result: result, DropAfter: "tools/list"},
				testkit.LegacyReplayConnection{Result: result})
			l := h.Launch(args...)
			initializeRawLegacy(t, l)

			crashLegacyReplayFake(t, l, 1, deadline)
			l.WriteLine(rawPing(`"after"`))
			announced := methodsOf(t, readUntilResponse(t, l, `"after"`))

			assert.Equal(t, tc.want, announced, "the host got exactly the advertised announcements before the later response")
			assert.NotContains(t, announced, toolsListChanged)
			assert.Equal(t, 2, fake.Accepted())
			assert.Zero(t, h.Spawns())
		})
	}
}
