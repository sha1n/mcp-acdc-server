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

func scriptedAcknowledgement(id string) string {
	return `{"jsonrpc":"2.0","method":"` + methodAcknowledged + `","params":{"_meta":{"` + metaKeySubscriptionID + `":` + id +
		`},"notifications":{"resourcesListChanged":true,"promptsListChanged":true}}}` + "\n"
}

// The replacement is bound only after the old shared server exited and while
// the recovery is held before its dial, so the recovery reaches it. It
// consumes both re-issued streams before it drops, which makes the drop an
// end of stream on every platform; "between two acknowledgements" acknowledges
// the first stream before the drop.
func TestSessionListenRepair_ADropDuringTheReissueFailsTheRecovery(t *testing.T) {
	tests := []struct {
		name  string
		reply string
	}{
		{name: "before the first acknowledgement"},
		{name: "between two acknowledgements", reply: scriptedAcknowledgement(listenID)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deadline := time.Now().Add(testkit.SharedServerDeadline)
			h := testkit.NewSharedServerHarness(t)
			l, gate := h.LaunchGatingRecovery(recoveryLaunchArgs(listenRepairContentDir(t))...)
			attachRecords(t, l, 1, deadline)
			id := l.SharedServerIdentity()
			l.WriteLine(modernListen(listenID))
			readAcknowledgement(t, l, listenID)
			l.WriteLine(modernListen("3"))
			readAcknowledgement(t, l, "3")

			crashIntoHeldRecovery(t, h, l, gate, deadline)
			h.RemoveSocket(id)
			accepted, consumed := h.ServeReissueThenDrop(id, testkit.ReissueDrop{Reads: 2, Reply: tt.reply})
			l.WriteLine(modernSearch("21", "outage"))
			l.WriteLine(modernListen("22"))
			l.WriteUnterminated("{")
			gate.Release()

			assert.Equal(t, connectionLostError("21")+connectionLostError("22"), string(l.ReadToEOF()),
				"each held request, the held listen request included, gets the connection-lost error in held order; the open streams get nothing")
			err := l.Wait(time.Until(deadline))
			require.Error(t, err, "a failed recovery ends the runner with an error")
			assert.Contains(t, err.Error(), failedRecoveryErrorPrefix+" (dropped_in_sequence)")
			requireNoProxyFailureLine(t, l)
			assert.Equal(t, []string{attachMessage, crashWarningMessage, attachMessage}, recoveryLifecycleOf(t, l),
				"stderr has the two attach lines and the one crash warning only")
			assert.Equal(t, []string{modernListen(listenID) + "\n", modernListen("3") + "\n"}, consumed(),
				"the open streams were re-issued verbatim in send order")
			assert.Equal(t, 1, accepted(), "no rediscovery dialled again")
			assert.Equal(t, 1, h.Spawns(), "nothing was spawned at recovery")
			assert.Zero(t, h.FallbackWarnings())
		})
	}
}

func requireNoProxyFailureLine(t *testing.T, l *testkit.LaunchUnderTest) {
	t.Helper()
	for _, record := range stderrRecords(t, l, slogtext.Record{}) {
		assert.NotEqual(t, "ERROR", record["level"], "the proxy writes no error line for the failure: %v", record)
		assert.NotContains(t, record["msg"], failedRecoveryErrorPrefix)
		assert.NotEqual(t, perSessionStartupMessage, record["msg"], "no per-session server was built")
	}
	assert.Empty(t, l.FallbackWarnings(), "a failed recovery never falls back: %s", l.Stderr())
}
