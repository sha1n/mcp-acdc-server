//go:build unix

package sharedserver

import (
	"context"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The search is sent once the crash warning is on stderr, so it travels the
// recovered connection, and its response follows the replay: every line the
// launch wrote to the host before it is recorded by then.
func TestSessionLegacyReplay_SearchAfterALossSucceedsWithoutTheHostReinitialising(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	h := testkit.NewSharedServerHarness(t)
	received := h.RecordReceived()
	l := h.Launch(recoveryLaunchArgs(newSessionProxyContentDir(t))...)
	session, traffic := l.ConnectLegacy(ctx)
	attachRecords(t, l, 1, deadline)

	h.StopSharedServers()
	awaitStderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage}, 1, deadline)
	requireSearchSucceeds(ctx, t, session)

	initializeIDs := requestIDsOf(t, traffic.ToLaunch(), "initialize")
	require.Len(t, initializeIDs, 1, "the host initialized once")
	assert.Len(t, responsesTo(t, traffic.FromLaunch(), initializeIDs[0]), 1,
		"the host saw only the response of the first shared server to its initialize")
	assert.Equal(t, 2, countReceived(received.Received(), "initialize"),
		"the replacement shared server received the replayed initialize")
	assert.Equal(t, 2, h.Builds(), "the search was answered by a new shared server")
}
