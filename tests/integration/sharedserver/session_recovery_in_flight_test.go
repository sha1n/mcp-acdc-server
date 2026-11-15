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

func TestSessionRecovery_InFlightSearchGetsTheConnectionLostErrorAndALaterRequestSucceedsOnANewSharedServer(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	args := recoveryLaunchArgs(newSessionProxyContentDir(t))
	h := testkit.NewSharedServerHarness(t)
	held := h.HoldSearches()
	l := h.Launch(args...)
	first := attachRecords(t, l, 1, deadline)[0]

	l.WriteLine(modernSearch("7", testkit.HeldSearchQuery))
	held.AwaitReceived()
	h.StopSharedServers()

	assert.Equal(t, connectionLostError("7"), string(l.ReadLine()), "the in-flight search is answered with exactly the connection-lost error")
	l.WriteLine(modernSearch("8", "authentication"))
	resp := decodeRawResponse(t, l.ReadLine())
	assert.JSONEq(t, "8", string(resp.ID))
	assert.Nil(t, resp.Error, "the later search succeeds")
	assert.NotEmpty(t, resp.Result)

	attaches := attachRecords(t, l, 2, deadline)
	require.Len(t, attaches, 2)
	second := attaches[1]
	assert.NotEqual(t, first["session"], second["session"], "the recovered session has a new session identifier")
	assert.Equal(t, first["log"], second["log"], "the new shared server serves the same identity")
	assert.Equal(t, 2, h.Builds(), "the recovery built a new shared server")
	starts := serverLogRecords(t, second["log"], slogtext.Record{"msg": serverStartupMessage})
	assert.Len(t, starts, 2, "a second shared server started for the identity")
	attached := awaitServerLogRecords(t, second["log"], slogtext.Record{"msg": sessionAttachedMessage}, 2, deadline)
	assert.Equal(t, []string{first["session"], second["session"]}, sessionsOf(attached))
	requireNoProxyRecordsInServerLog(t, second["log"])
	assert.Zero(t, h.FallbackWarnings())
}
