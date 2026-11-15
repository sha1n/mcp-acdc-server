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

const sessionEndedWithErrorMessage = "Session ended with an error"

func TestSessionRecovery_MessagesWrittenDuringTheOutageAreDeliveredInOrderAfterRecovery(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	h := testkit.NewSharedServerHarness(t)
	held := h.HoldSearches()
	received := h.RecordReceived()
	gate := h.GateBuildsAfter(1)
	l := h.Launch(recoveryLaunchArgs(newSessionProxyContentDir(t))...)
	attachRecords(t, l, 1, deadline)

	l.WriteLine(modernSearch("5", testkit.HeldSearchQuery))
	held.AwaitReceived()
	h.StopSharedServers()
	require.Equal(t, connectionLostError("5"), string(l.ReadLine()))
	warning := awaitStderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage}, 1, deadline)
	assert.Equal(t, "1", warning[0]["in_flight"])
	gate.AwaitHeld()

	// Notifications are the only messages the SDK hands to the receiving
	// middleware in read order, so leading with the cancellation makes both
	// later searches provably read after it.
	outage := []string{
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":5,"reason":"host gave up"}}`,
		modernSearch("10", "outage-first"),
		modernSearch("11", "outage-second"),
	}
	for _, line := range outage {
		l.WriteLine(line)
	}
	// Held lines are counted when the recovery writes them, so the last one
	// must be taken before the new shared server can be ready.
	probe := modernSearch("12", "authentication")
	l.WriteUnterminated(probe[:1])
	gate.Release()

	recovered := awaitStderrRecords(t, l, slogtext.Record{"level": "INFO", "msg": recoveredMessage}, 1, deadline)
	assert.Equal(t, "3", recovered[0]["held"], "every outage line was held and written to the new shared server")
	l.WriteLine(probe[1:])

	results := readResponsesByID(t, l, 3)
	for _, id := range []string{"10", "11", "12"} {
		require.Contains(t, results, id, "every request gets a response from the new shared server")
		assert.Nil(t, results[id].Error, "request %s is answered by the shared server, not with a synthesized error", id)
		assert.NotEmpty(t, results[id].Result, "request %s", id)
	}

	got := received.Received()
	require.Len(t, got, 4, "the new shared server received each line once: %v", got)
	assert.Equal(t, testkit.ReceivedMessage{Method: "notifications/cancelled", Detail: "5"}, got[0],
		"the cancellation reaches the new shared server first, verbatim with its request id")
	assert.ElementsMatch(t, []testkit.ReceivedMessage{
		{Method: "tools/call", Detail: "outage-first"},
		{Method: "tools/call", Detail: "outage-second"},
		{Method: "tools/call", Detail: "authentication"},
	}, got[1:])

	attaches := attachRecords(t, l, 2, deadline)
	assert.Len(t, attaches, 2)
	assert.Equal(t, 2, h.Builds(), "the recovery built a new shared server")
	assert.Len(t, stderrRecords(t, l, slogtext.Record{"msg": crashWarningMessage}), 1)
	assert.Zero(t, h.FallbackWarnings())
}

// The line is an array, so it is not a request, yet its first element is a
// well-formed call with an id: a session proxy that tracked requests inside
// non-object lines would answer id 9 with the connection-lost error. The
// trailing 0 makes the SDK reject the whole line, which ends the session.
func TestSessionRecovery_NonObjectHostLineIsForwardedAndNeverGetsASynthesizedError(t *testing.T) {
	nonObject := `[{"jsonrpc":"2.0","id":9,"method":"tools/list","params":{` + modernMeta + `}},0]`
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	h := testkit.NewSharedServerHarness(t)
	l := h.Launch(recoveryLaunchArgs(newSessionProxyContentDir(t))...)
	first := attachRecords(t, l, 1, deadline)[0]

	l.WriteLine(nonObject)

	ended := awaitServerLogRecords(t, first["log"], slogtext.Record{"msg": sessionEndedWithErrorMessage, "session": first["session"]}, 1, deadline)
	assert.Contains(t, ended[0]["error"], `cannot unmarshal "0"`, "the shared server read the whole line and rejected its trailing element")
	second := attachRecords(t, l, 2, deadline)[1]
	assert.NotEqual(t, first["session"], second["session"], "the session recovers with a new session identifier")
	assert.Equal(t, first["log"], second["log"], "the session recovers onto the same shared server")
	warnings := stderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage})
	require.Len(t, warnings, 1)
	assert.Equal(t, "0", warnings[0]["in_flight"], "the non-object line is not in flight")

	l.WriteLine(modernSearch("3", "authentication"))
	resp := decodeRawResponse(t, l.ReadLine())
	assert.JSONEq(t, "3", string(resp.ID), "nothing was synthesized for the non-object line")
	assert.Nil(t, resp.Error, "a later request succeeds")
	assert.NotEmpty(t, resp.Result)
	assert.Equal(t, 1, h.Builds(), "the session recovers onto the same shared server")
	attached := awaitServerLogRecords(t, second["log"], slogtext.Record{"msg": sessionAttachedMessage}, 2, deadline)
	assert.Equal(t, []string{first["session"], second["session"]}, sessionsOf(attached))
	assert.Zero(t, h.FallbackWarnings())
}

// go-sdk reads a request only when its method is a string, so it rejects
// this line and ends the session. The line is not a request, so the session
// proxy must not answer id 17 with the connection-lost error.
func TestSessionRecovery_LineWithANonStringMethodIsForwardedAndNeverGetsASynthesizedError(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	h := testkit.NewSharedServerHarness(t)
	l := h.Launch(recoveryLaunchArgs(newSessionProxyContentDir(t))...)
	first := attachRecords(t, l, 1, deadline)[0]

	l.WriteLine(`{"jsonrpc":"2.0","id":17,"method":7}`)

	awaitServerLogRecords(t, first["log"], slogtext.Record{"msg": sessionEndedWithErrorMessage, "session": first["session"]}, 1, deadline)
	second := attachRecords(t, l, 2, deadline)[1]
	assert.NotEqual(t, first["session"], second["session"], "the session recovers with a new session identifier")
	assert.Equal(t, first["log"], second["log"], "the session recovers onto the same shared server")
	warnings := stderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage})
	require.Len(t, warnings, 1)
	assert.Equal(t, "0", warnings[0]["in_flight"], "the line with a non-string method is not in flight")

	requireNextLineAnswersALaterRequest(t, l, "18")
	assert.Equal(t, 1, h.Builds(), "the session recovers onto the same shared server")
	assert.Zero(t, h.FallbackWarnings())
}

func readResponsesByID(t *testing.T, l *testkit.LaunchUnderTest, n int) map[string]rawResponse {
	t.Helper()
	responses := make(map[string]rawResponse, n)
	for range n {
		resp := decodeRawResponse(t, l.ReadLine())
		responses[string(resp.ID)] = resp
	}
	return responses
}
