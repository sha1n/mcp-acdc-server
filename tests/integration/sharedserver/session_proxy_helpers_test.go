//go:build unix

package sharedserver

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

const lossPollInterval = 10 * time.Millisecond

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
	return runWinningColdStarts(t, 1, attempt)
}

// runWinningColdStarts generalises runWinningColdStart to attempts that
// build shared servers by design: every build beyond those must be a
// candidate that lingered out before any session reached it.
func runWinningColdStarts(t *testing.T, builds int, attempt coldStartAttempt) *testkit.SharedServerHarness {
	t.Helper()
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	for attempts := 1; ; attempts++ {
		h := testkit.NewSharedServerHarness(t)
		attempt(t, h)
		if h.Builds() <= builds && h.FallbackWarnings() == 0 {
			return h
		}
		requireLostColdStarts(t, h, builds, deadline)
		require.True(t, time.Now().Before(deadline), "no attempt built only %d shared servers in %d attempts", builds, attempts)
	}
}

func requireLostColdStarts(t *testing.T, h *testkit.SharedServerHarness, builds int, deadline time.Time) {
	t.Helper()
	extraBuilds := h.Builds() - builds
	fallbacks := h.FallbackWarnings()
	// A launch falls back only after two of its candidates lingered out, in
	// place of the one it builds by design.
	require.GreaterOrEqual(t, extraBuilds, fallbacks, "a fallback-served launch is lost only after its first candidate lingered out")
	// No shared server holds a fallback-served session, so the last
	// candidate of each such launch may have lingered out as well.
	lingeredOut := requireLingerExits(t, awaitBuiltExits(t, h, deadline, extraBuilds))
	require.GreaterOrEqual(t, lingeredOut, extraBuilds, "every extra build follows a candidate that lingered out")
	require.LessOrEqual(t, lingeredOut, extraBuilds+fallbacks, "only lost candidates lingered out")
}

// awaitBuiltExits returns the exits of the built candidates once at least
// want of them have been recorded. A candidate records its exit only after
// it has closed its listener, so a session proxy can move on before the
// record exists.
func awaitBuiltExits(t *testing.T, h *testkit.SharedServerHarness, deadline time.Time, want int) []testkit.SharedServerExit {
	t.Helper()
	if exits := builtExits(h); len(exits) >= want {
		return exits
	}
	require.Eventually(t, func() bool { return len(builtExits(h)) >= want }, time.Until(deadline), lossPollInterval,
		"a lost attempt must have %d built candidates that lingered out", want)
	return builtExits(h)
}

func builtExits(h *testkit.SharedServerHarness) []testkit.SharedServerExit {
	return slices.DeleteFunc(h.SharedServerExits(), func(e testkit.SharedServerExit) bool { return !e.Built })
}

// requireLingerExits requires every exit to be a normal linger exit and
// returns how many there are.
func requireLingerExits(t *testing.T, exits []testkit.SharedServerExit) int {
	t.Helper()
	for _, exit := range exits {
		require.NoError(t, exit.Err, "a lost attempt's candidate must end as a normal linger exit")
		require.GreaterOrEqual(t, exit.ExitedAt.Sub(exit.SpawnedAt), config.MinSharedServerLinger,
			"a lost attempt's candidate exited before the linger elapsed")
	}
	return len(exits)
}
