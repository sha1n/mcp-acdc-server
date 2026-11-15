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

// The shared server ends a cancelled stream with a result, which reaches the
// host only after the server consumed the cancellation, so the proxy wrote it
// whole. The response to a later request is the first line after the loss:
// an announcement would precede it.
func TestSessionListenRepair_CancelledStreamIsNotReissuedAndNothingIsAnnounced(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	h := testkit.NewSharedServerHarness(t)
	received := h.RecordReceived()
	l := h.Launch(recoveryLaunchArgs(listenRepairContentDir(t))...)
	attachRecords(t, l, 1, deadline)

	l.WriteLine(modernListen(listenID))
	readAcknowledgement(t, l, listenID)
	l.WriteLine(cancelRequest(listenID))
	end := decodeHostLine(t, l.ReadLine())
	require.True(t, end.answers(listenID), "the cancelled stream ends: %s", end.raw)
	require.Nil(t, end.Error, "the stream ends with a result: %s", end.raw)
	h.StopSharedServers()
	awaitStderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage}, 1, deadline)

	requireModernSearchSucceeds(t, l, "8")
	assert.Equal(t, 1, listensReceived(received), "the cancelled stream was never re-issued")
	assert.Len(t, attachRecords(t, l, 2, deadline), 2)
	assert.Equal(t, 2, h.Builds(), "the recovery built a new shared server")
	assert.Zero(t, h.FallbackWarnings())
}
