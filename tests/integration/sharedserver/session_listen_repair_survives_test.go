//go:build unix

package sharedserver

import (
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
)

// The stream is open once the host saw its acknowledgement. Every host line
// is read in order up to a delivery the new shared server makes on the same
// stream after the recovery, so a second acknowledgement or an end of the
// stream would have been seen before it.
func TestSessionListenRepair_OpenStreamSurvivesASharedServerLossAndDeliversLaterChanges(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	contentDir := listenRepairContentDir(t)
	h := testkit.NewSharedServerHarness(t)
	received := h.RecordReceived()
	l := h.Launch(recoveryLaunchArgs(contentDir)...)
	attachRecords(t, l, 1, deadline)

	l.WriteLine(modernListen(listenID))
	readAcknowledgement(t, l, listenID)
	h.StopSharedServers()
	awaitStderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage}, 1, deadline)

	assert.Equal(t, proxyAnnouncement(resourcesKind, listenID), string(l.ReadLine()),
		"the first line after the loss is the proxy's resources announcement on the stream")
	assert.Equal(t, proxyAnnouncement(promptsKind, listenID), string(l.ReadLine()))
	later := awaitStreamDelivery(t, l, contentDir, listenID, deadline)
	for _, msg := range later {
		assert.False(t, msg.isAcknowledgement(), "the host sees exactly one acknowledgement: %v", rawLines(later))
		assert.False(t, msg.answers(listenID), "the stream is never answered: %v", rawLines(later))
	}

	assert.Equal(t, 2, listensReceived(received), "the stream was sent once to each shared server")
	assert.Len(t, attachRecords(t, l, 2, deadline), 2)
	assert.Equal(t, 2, h.Builds(), "the recovery built a new shared server")
	assert.Len(t, stderrRecords(t, l, slogtext.Record{"msg": crashWarningMessage}), 1)
	assert.Zero(t, h.FallbackWarnings())
}
