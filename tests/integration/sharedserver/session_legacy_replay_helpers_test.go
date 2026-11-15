//go:build unix

package sharedserver

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	replayMismatchWarning = "Replayed initialize result differs from the one the host saw"
	resourcesListChanged  = "notifications/resources/list_changed"
	promptsListChanged    = "notifications/prompts/list_changed"
	toolsListChanged      = "notifications/tools/list_changed"
)

type hostMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func decodeHostMessage(t *testing.T, line string) hostMessage {
	t.Helper()
	var msg hostMessage
	require.NoError(t, json.Unmarshal([]byte(line), &msg), "line: %s", line)
	return msg
}

// requestIDsOf returns the ids of the requests for method among lines.
func requestIDsOf(t *testing.T, lines []string, method string) []string {
	t.Helper()
	var ids []string
	for _, line := range lines {
		if msg := decodeHostMessage(t, line); msg.Method == method && msg.ID != nil {
			ids = append(ids, string(msg.ID))
		}
	}
	return ids
}

// responsesTo returns the responses among lines whose id is id.
func responsesTo(t *testing.T, lines []string, id string) []string {
	t.Helper()
	var responses []string
	for _, line := range lines {
		if msg := decodeHostMessage(t, line); msg.Method == "" && string(msg.ID) == id {
			responses = append(responses, line)
		}
	}
	return responses
}

// methodsOf returns the method of every request and notification among
// lines, in order.
func methodsOf(t *testing.T, lines []string) []string {
	t.Helper()
	var methods []string
	for _, line := range lines {
		if msg := decodeHostMessage(t, line); msg.Method != "" {
			methods = append(methods, msg.Method)
		}
	}
	return methods
}

func countReceived(received []testkit.ReceivedMessage, method string) int {
	n := 0
	for _, m := range received {
		if m.Method == method {
			n++
		}
	}
	return n
}

func requireSearchSucceeds(ctx context.Context, t *testing.T, session *mcp.ClientSession) {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search", Arguments: map[string]any{"query": "authentication"}})
	require.NoError(t, err)
	require.False(t, result.IsError, "the search succeeds: %v", result.Content)
	require.NotEmpty(t, result.Content)
}

const (
	rawLegacyInitialize  = `{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"raw-legacy-host","version":"1.0.0"}}}`
	rawLegacyInitialized = `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`
	allListChanges       = `{"tools":{"listChanged":true},"resources":{"listChanged":true},"prompts":{"listChanged":true}}`
)

func legacyResult(protocolVersion, capabilities, instructions string) string {
	return fmt.Sprintf(`{"protocolVersion":%q,"capabilities":%s,"instructions":%q,"serverInfo":{"name":"legacy-replay-fake","version":"1.0.0"}}`,
		protocolVersion, capabilities, instructions)
}

func rawPing(id string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"ping"}`, id)
}

// initializeRawLegacy performs the legacy handshake with raw lines. The ping
// that follows it is answered only after notifications/initialized was
// written to the shared server, so the handshake is cached by then.
func initializeRawLegacy(t *testing.T, l *testkit.LaunchUnderTest) {
	t.Helper()
	l.WriteLine(rawLegacyInitialize)
	require.Nil(t, decodeHostMessage(t, string(l.ReadLine())).Error, "the initialize succeeds")
	l.WriteLine(rawLegacyInitialized)
	l.WriteLine(rawPing(`"handshake-done"`))
	requireResponse(t, l, `"handshake-done"`)
}

func requireResponse(t *testing.T, l *testkit.LaunchUnderTest, id string) {
	t.Helper()
	msg := decodeHostMessage(t, string(l.ReadLine()))
	require.JSONEq(t, id, string(msg.ID))
	require.Nil(t, msg.Error, "request %s succeeds", id)
}

// crashLegacyReplayFake has the fake drop its connection right after it
// answered a tools/list, the drop scripted as DropAfter "tools/list", and
// returns once the proxy reported the crash.
func crashLegacyReplayFake(t *testing.T, l *testkit.LaunchUnderTest, crashes int, deadline time.Time) {
	t.Helper()
	l.WriteLine(fmt.Sprintf(`{"jsonrpc":"2.0","id":"crash-%d","method":"tools/list"}`, crashes))
	requireResponse(t, l, fmt.Sprintf(`"crash-%d"`, crashes))
	awaitStderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage}, crashes, deadline)
}

// readUntilResponse returns the host lines written before the response to
// id, which it consumes.
func readUntilResponse(t *testing.T, l *testkit.LaunchUnderTest, id string) []string {
	t.Helper()
	var before []string
	for {
		line := string(l.ReadLine())
		if msg := decodeHostMessage(t, line); msg.Method == "" && string(msg.ID) == id {
			require.Nil(t, msg.Error, "request %s succeeds", id)
			return before
		}
		before = append(before, line)
	}
}

// requireFailedReplay is requireFailedRecovery for a recovery that attached
// to the replacement fake before its sequence failed: nothing was dialled or
// spawned after that attach.
func requireFailedReplay(t *testing.T, h *testkit.SharedServerHarness, l *testkit.LaunchUnderTest, fake *testkit.LegacyReplayServer, cause string) {
	t.Helper()
	err := l.Wait(testkit.SharedServerDeadline)
	require.Error(t, err, "a failed recovery ends the runner with an error")
	assert.Contains(t, err.Error(), failedRecoveryErrorPrefix+" ("+cause+")")

	for _, record := range stderrRecords(t, l, slogtext.Record{}) {
		assert.NotEqual(t, "ERROR", record["level"], "the proxy writes no error line for the failure: %v", record)
		assert.NotContains(t, record["msg"], failedRecoveryErrorPrefix)
		assert.NotEqual(t, perSessionStartupMessage, record["msg"], "no per-session server was built")
	}
	assert.Zero(t, h.FallbackWarnings())
	assert.Equal(t, []string{attachMessage, crashWarningMessage, attachMessage}, recoveryLifecycleOf(t, l),
		"stderr has the two attach lines and the one crash warning only")
	assert.Equal(t, 2, fake.Accepted(), "no rediscovery after the failed sequence")
	assert.Zero(t, h.Spawns())
}
