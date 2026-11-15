//go:build unix

package sharedserver

import (
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
)

// Every host line after the loss is read in order up to the response to a
// later request, so a connection-lost error for the stream would be among
// them.
func TestSessionListenRepair_OpenStreamIsNeverAnsweredWithTheConnectionLostErrorWhileAnInFlightRequestIs(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	h := testkit.NewSharedServerHarness(t)
	held := h.HoldSearches()
	l := h.Launch(recoveryLaunchArgs(listenRepairContentDir(t))...)
	attachRecords(t, l, 1, deadline)

	l.WriteLine(modernListen(listenID))
	readAcknowledgement(t, l, listenID)
	l.WriteLine(modernSearch("7", testkit.HeldSearchQuery))
	held.AwaitReceived()
	h.StopSharedServers()

	assert.Equal(t, connectionLostError("7"), string(l.ReadLine()), "the in-flight request gets the connection-lost error")
	assert.Equal(t, proxyAnnouncement(resourcesKind, listenID), string(l.ReadLine()), "the open stream gets announcements instead")
	assert.Equal(t, proxyAnnouncement(promptsKind, listenID), string(l.ReadLine()))
	requireModernSearchSucceeds(t, l, "8")
	assert.Len(t, attachRecords(t, l, 2, deadline), 2)
	assert.Zero(t, h.FallbackWarnings())
}
