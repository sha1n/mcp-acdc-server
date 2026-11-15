//go:build unix

package sharedserver

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serverAnswer is what the real shared server does with a search carrying a
// given id.
type serverAnswer int

const (
	// answersWithItsEcho: a response whose id is the go-sdk echo of the id.
	answersWithItsEcho serverAnswer = iota
	// answersNothing: go-sdk reads a null id as a notification.
	answersNothing
	// endsTheSession: go-sdk cannot decode the id and ends the session.
	endsTheSession
)

func TestSessionRecovery_RequestIDFormsAgainstTheRealSharedServer(t *testing.T) {
	cases := []struct {
		name   string
		id     string
		answer serverAnswer
		// echo is empty where it depends on the platform's float to int64
		// conversion; the go-sdk echo is then the only reference.
		echo string
	}{
		{name: "string control", id: `"abc"`, echo: `"abc"`},
		{name: "fraction", id: `1.5`, echo: `1`},
		{name: "exponent", id: `1e2`, echo: `100`},
		{name: "beyond 2^53", id: `9007199254740993`, echo: `9007199254740992`},
		{name: "beyond int64", id: `1e300`},
		{name: "beyond float64", id: `1e400`, answer: endsTheSession},
		{name: "null", id: `null`, answer: answersNothing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deadline := time.Now().Add(testkit.SharedServerDeadline)
			h := testkit.NewSharedServerHarness(t)
			held := h.HoldSearches()
			l := h.Launch(recoveryLaunchArgs(newSessionProxyContentDir(t))...)
			attachRecords(t, l, 1, deadline)

			switch tc.answer {
			case answersWithItsEcho:
				echo := goSDKEcho(t, tc.id)
				if tc.echo != "" {
					require.Equal(t, tc.echo, echo, "go-sdk echo")
				}
				requireAnsweredIDIsNotErroredOnACrash(t, h, l, tc.id, echo)
				requireInFlightIDGetsOneConnectionLostError(t, h, l, held, tc.id)
			case answersNothing:
				requireNullIDIsNeitherAnsweredNorErrored(t, h, l, held, tc.id)
			case endsTheSession:
				requireUndecodableIDEndsTheSessionWithOneConnectionLostError(t, h, l, tc.id)
			}
			assert.Zero(t, h.FallbackWarnings())
		})
	}
}

// requireAnsweredIDIsNotErroredOnACrash asserts the real server answers a
// search with id using exactly echo, and that a crash after that answer
// synthesizes no connection-lost error for it.
func requireAnsweredIDIsNotErroredOnACrash(t *testing.T, h *testkit.SharedServerHarness, l *testkit.LaunchUnderTest, id, echo string) {
	t.Helper()
	l.WriteLine(modernSearch(id, "authentication"))
	resp := decodeRawResponse(t, l.ReadLine())
	assert.Equal(t, echo, string(resp.ID), "the shared server's response id bytes")
	assert.Nil(t, resp.Error)
	assert.NotEmpty(t, resp.Result)

	h.StopSharedServers()

	requireNextLineAnswersALaterRequest(t, l, `"after-first-crash"`)
	requireCrashWarnings(t, l, "0")
}

// requireInFlightIDGetsOneConnectionLostError asserts a search with id that
// the shared server has read whole and holds at a crash is answered with
// exactly one connection-lost error echoing the bytes the host sent.
func requireInFlightIDGetsOneConnectionLostError(t *testing.T, h *testkit.SharedServerHarness, l *testkit.LaunchUnderTest, held *testkit.HeldSearches, id string) {
	t.Helper()
	l.WriteLine(modernSearch(id, testkit.HeldSearchQuery))
	held.AwaitReceived()
	h.StopSharedServers()

	assert.Equal(t, connectionLostError(id), string(l.ReadLine()))
	requireNextLineAnswersALaterRequest(t, l, `"after-second-crash"`)
	requireCrashWarnings(t, l, "0", "1")
	assert.Equal(t, 3, h.Builds(), "each crash built a new shared server")
}

// requireNullIDIsNeitherAnsweredNorErrored: go-sdk never hands a null-id
// search to the tool handler, so no held search can show it was read.
// Instead a later request on the same connection shows it: the shared
// server reads its session's lines in order, so a response to, or the
// receipt of, a later line means the null-id line was read before it.
func requireNullIDIsNeitherAnsweredNorErrored(t *testing.T, h *testkit.SharedServerHarness, l *testkit.LaunchUnderTest, held *testkit.HeldSearches, id string) {
	t.Helper()
	l.WriteLine(modernSearch(id, "authentication"))
	requireNextLineAnswersALaterRequest(t, l, `"after-null"`)
	h.StopSharedServers()
	requireNextLineAnswersALaterRequest(t, l, `"after-first-crash"`)
	requireCrashWarnings(t, l, "0")

	l.WriteLine(modernSearch(id, testkit.HeldSearchQuery))
	l.WriteLine(modernSearch(`"held-after-null"`, testkit.HeldSearchQuery))
	held.AwaitReceived()
	h.StopSharedServers()

	assert.Equal(t, connectionLostError(`"held-after-null"`), string(l.ReadLine()), "only the request after the null-id line is answered with the connection-lost error")
	requireNextLineAnswersALaterRequest(t, l, `"after-second-crash"`)
	requireCrashWarnings(t, l, "0", "1")
	assert.Equal(t, 3, h.Builds(), "each crash built a new shared server")
}

// requireUndecodableIDEndsTheSessionWithOneConnectionLostError: go-sdk
// cannot decode the id, so it ends the session on reading the line; the
// request is then in flight at the crash it caused, and the proxy recovers
// onto the same shared server.
func requireUndecodableIDEndsTheSessionWithOneConnectionLostError(t *testing.T, h *testkit.SharedServerHarness, l *testkit.LaunchUnderTest, id string) {
	t.Helper()
	l.WriteLine(modernSearch(id, "authentication"))

	assert.Equal(t, connectionLostError(id), string(l.ReadLine()))
	requireNextLineAnswersALaterRequest(t, l, `"after-session-end"`)
	requireCrashWarnings(t, l, "1")
	assert.Equal(t, 1, h.Builds(), "the session recovered onto the same shared server")
}

// requireNextLineAnswersALaterRequest sends a search with id and asserts the
// next host line is its successful response. After a crash, that response
// comes from the new connection, so every line the old one produced and
// every connection-lost error has reached the host before it.
func requireNextLineAnswersALaterRequest(t *testing.T, l *testkit.LaunchUnderTest, id string) {
	t.Helper()
	l.WriteLine(modernSearch(id, "authentication"))
	resp := decodeRawResponse(t, l.ReadLine())
	require.Equal(t, id, string(resp.ID), "the next host line answers the later request")
	assert.Nil(t, resp.Error)
	assert.NotEmpty(t, resp.Result)
}

// requireCrashWarnings asserts the crash warnings so far report inFlight, in
// order. It is called after a response that followed the last crash's
// attach line, which is written after that crash's warning.
func requireCrashWarnings(t *testing.T, l *testkit.LaunchUnderTest, inFlight ...string) {
	t.Helper()
	warnings := stderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage})
	got := make([]string, len(warnings))
	for i, w := range warnings {
		got[i] = w["in_flight"]
	}
	assert.Equal(t, inFlight, got, "in_flight of each crash warning")
}

// goSDKEcho is the id the shared server's go-sdk writes in its response to
// a request carrying id.
func goSDKEcho(t *testing.T, id string) string {
	t.Helper()
	msg, err := jsonrpc.DecodeMessage([]byte(`{"jsonrpc":"2.0","id":` + id + `,"method":"ping"}`))
	require.NoError(t, err)
	request, ok := msg.(*jsonrpc.Request)
	require.True(t, ok)
	encoded, err := jsonrpc.EncodeMessage(&jsonrpc.Response{ID: request.ID, Result: json.RawMessage(`{}`)})
	require.NoError(t, err)
	var response struct {
		ID json.RawMessage `json:"id"`
	}
	require.NoError(t, json.Unmarshal(encoded, &response))
	return string(response.ID)
}
