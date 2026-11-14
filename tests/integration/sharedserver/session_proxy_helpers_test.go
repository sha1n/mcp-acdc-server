//go:build unix

package sharedserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

const (
	rawInitializeRequest    = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"raw-host","version":"1.0.0"}}}`
	rawInitializedNotice    = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	rawToolsListRequestID2  = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	rawToolsListRequestID3  = `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`
	rawSearchCallRequestID3 = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"query":"authentication"}}}`
)

type rawResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func decodeRawResponse(t *testing.T, line []byte) rawResponse {
	t.Helper()
	var resp rawResponse
	require.NoError(t, json.Unmarshal(line, &resp), "line: %s", line)
	return resp
}

func connectAndListTools(t *testing.T, l *testkit.LaunchUnderTest) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testkit.SharedServerDeadline)
	defer cancel()
	session := l.Connect(ctx)
	_, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	return session
}

// coldStartAttempt runs one cold start with a fresh harness, so a lost
// attempt never shares an identity directory with the next.
type coldStartAttempt func(t *testing.T, h *testkit.SharedServerHarness)

// runWinningColdStart repeats attempt while its first built candidate
// lingered out before any session reached it: the initial linger starts at
// bind, so a session proxy descheduled for the first second after bind makes
// it build again (#196), or falls back to a per-session server once it has
// lost twice. Every lost attempt must show exactly that, and a winning
// attempt must have served every session through a shared server.
func runWinningColdStart(t *testing.T, attempt coldStartAttempt) *testkit.SharedServerHarness {
	t.Helper()
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	for attempts := 1; ; attempts++ {
		h := testkit.NewSharedServerHarness(t)
		attempt(t, h)
		if h.Builds() <= 1 && h.FallbackWarnings() == 0 {
			return h
		}
		requireLostToTheInitialLinger(t, h)
		require.True(t, time.Now().Before(deadline), "no cold start built only once in %d attempts", attempts)
	}
}

func requireLostToTheInitialLinger(t *testing.T, h *testkit.SharedServerHarness) {
	t.Helper()
	builds := h.Builds()
	wantLingeredOut := []int{builds - 1}
	if h.FallbackWarnings() > 0 {
		require.GreaterOrEqual(t, builds, 2, "a fallback-served attempt is lost only after its first candidate lingered out")
		// No shared server holds a fallback-served session, so the last
		// candidate may have lingered out as well.
		wantLingeredOut = append(wantLingeredOut, builds)
	}
	lingeredOut := 0
	for _, exit := range h.SharedServerExits() {
		if !exit.Built {
			continue
		}
		require.NoError(t, exit.Err, "a lost attempt's candidate must end as a normal linger exit")
		require.GreaterOrEqual(t, exit.ExitedAt.Sub(exit.SpawnedAt), config.MinSharedServerLinger,
			"a lost attempt's candidate exited before the linger elapsed")
		lingeredOut++
	}
	require.Contains(t, wantLingeredOut, lingeredOut, "every extra build follows a candidate that lingered out")
}
