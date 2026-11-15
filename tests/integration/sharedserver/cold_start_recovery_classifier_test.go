//go:build unix

package sharedserver

import (
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
)

// No scheduling loss of #196 drops a session after its ready line, so an
// attempt in which a session recovered is never a winner nor a loss.
func TestClassifyColdStart_FailsAnAttemptInWhichASessionRecovered(t *testing.T) {
	for name, e := range map[string]coldStartEvidence{
		"within the designed builds": {designedBuilds: 1, launches: 1, builds: 1, spawns: 1, crashes: 1},
		"beyond the designed builds": {designedBuilds: 1, launches: 1, builds: 2, spawns: 2, crashes: 1,
			exits: []testkit.SharedServerExit{{Built: true}}},
	} {
		assert.Equal(t, coldStartFailed, classifyColdStart(e).outcome, name)
	}
}

func TestCollectColdStartEvidence_CountsCrashWarningsOfEveryLaunch(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	held := h.HoldSearches()
	l := h.Launch(recoveryLaunchArgs(newSessionProxyContentDir(t))...)
	l.WriteLine(modernSearch("1", testkit.HeldSearchQuery))
	held.AwaitReceived()
	h.StopSharedServers()
	assert.Equal(t, connectionLostError("1"), string(l.ReadLine()))

	assert.Equal(t, 1, collectColdStartEvidence(h, 1).crashes)
}
