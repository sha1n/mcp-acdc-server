//go:build unix

package sharedserver

import (
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
)

// The replacement acknowledges the re-issued stream and then drops: the
// recovery sequence has no socket operation after the last acknowledgement,
// so the relay observes the drop as a new crash. The replacement removes its
// socket before the ready line, so the second recovery spawns a real shared
// server, which the old one no longer blocks, having exited.
func TestSessionListenRepair_ADropAfterTheLastAcknowledgementIsANewCrashThatRecovers(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	contentDir := listenRepairContentDir(t)
	h := testkit.NewSharedServerHarness(t)
	l, gate := h.LaunchGatingRecovery(recoveryLaunchArgs(contentDir)...)
	attachRecords(t, l, 1, deadline)
	id := l.SharedServerIdentity()
	l.WriteLine(modernListen(listenID))
	readAcknowledgement(t, l, listenID)

	crashIntoHeldRecovery(t, h, l, gate, deadline)
	h.RemoveSocket(id)
	accepted, consumed := h.ServeReissueThenDrop(id, testkit.ReissueDrop{Reads: 1, Reply: scriptedAcknowledgement(listenID), Unbind: true})
	gate.Release()

	for range 2 {
		assert.Equal(t, proxyAnnouncement(resourcesKind, listenID), string(l.ReadLine()), "each recovery announces on the stream")
		assert.Equal(t, proxyAnnouncement(promptsKind, listenID), string(l.ReadLine()))
	}
	later := awaitStreamDelivery(t, l, contentDir, listenID, deadline)
	for _, msg := range later {
		assert.False(t, msg.isAcknowledgement(), "the host sees exactly one acknowledgement: %v", rawLines(later))
		assert.False(t, msg.answers(listenID), "the stream is never answered: %v", rawLines(later))
	}

	attaches := attachRecords(t, l, 3, deadline)
	assert.Len(t, attaches, 3, "the drop after the last acknowledgement is one new crash, recovered")
	assert.Equal(t, "/var/log/acdc-test.log", attaches[1]["log"], "the second attach is to the replacement")
	assert.Equal(t, attaches[0]["log"], attaches[2]["log"], "the third attach is to a real shared server")
	assert.Equal(t, []string{modernListen(listenID) + "\n"}, consumed())
	assert.Equal(t, 1, accepted())
	assert.Equal(t, 2, h.Builds(), "the second recovery built a new shared server")
	assert.Len(t, stderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage}), 2)
	assert.Zero(t, h.FallbackWarnings())
}
