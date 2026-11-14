//go:build unix

package sharedserver

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
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

// runWinningColdStart repeats attempt while it lost its cold start to
// scheduling alone (#196): see classifyColdStart for what counts as such a
// loss. A winning attempt must have served every session through a shared
// server.
func runWinningColdStart(t *testing.T, attempt coldStartAttempt) *testkit.SharedServerHarness {
	t.Helper()
	return runWinningColdStarts(t, 1, attempt)
}

// runWinningColdStarts generalises runWinningColdStart to attempts that
// build shared servers by design.
func runWinningColdStarts(t *testing.T, builds int, attempt coldStartAttempt) *testkit.SharedServerHarness {
	t.Helper()
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	for attempts := 1; ; attempts++ {
		h := testkit.NewSharedServerHarness(t)
		attempt(t, h)
		verdict := classifyColdStart(collectColdStartEvidence(t, h, builds, deadline))
		switch verdict.outcome {
		case coldStartWon:
			return h
		case coldStartFailed:
			require.FailNow(t, "the cold start failed beyond a scheduling loss", verdict.reason)
		}
		require.True(t, time.Now().Before(deadline), "no attempt built only %d shared servers in %d attempts", builds, attempts)
	}
}

type coldStartOutcome int

const (
	coldStartWon coldStartOutcome = iota
	coldStartLost
	coldStartFailed
)

type coldStartVerdict struct {
	outcome coldStartOutcome
	reason  string
}

const (
	fallbackCauseDroppedTwice = "dropped_twice"
	fallbackCauseConnectWait  = "connect_wait"
)

// fallbackRecord is one fallback warning a launch wrote to its stderr.
type fallbackRecord struct {
	cause string
	at    time.Time
}

// coldStartEvidence is what one attempt observably did. Exits holds only
// built candidates.
type coldStartEvidence struct {
	designedBuilds int
	launches       int
	builds         int
	spawns         int
	exits          []testkit.SharedServerExit
	fallbacks      []fallbackRecord
}

func won() coldStartVerdict { return coldStartVerdict{outcome: coldStartWon} }

func lost() coldStartVerdict { return coldStartVerdict{outcome: coldStartLost} }

func failed(format string, args ...any) coldStartVerdict {
	return coldStartVerdict{outcome: coldStartFailed, reason: fmt.Sprintf(format, args...)}
}

// classifyColdStart decides whether an attempt won, lost its cold start to
// scheduling alone, or failed. The initial linger starts at bind, so a
// descheduled session proxy can miss its candidate in exactly two ways:
//   - dropped_twice: its connection landed twice in the backlog of a listener
//     closing at linger expiry, so it built at least once beyond design;
//   - connect_wait: its discovery was descheduled past the connect wait while
//     its one candidate bound, stayed unattached and lingered out.
//
// A loss is accepted only when every piece of evidence is consistent with
// those; any other outcome, including a fallback with any other cause, a
// candidate that exited with an error or before the linger elapsed, or a
// connect_wait fallback while a built candidate was still serving, fails.
func classifyColdStart(e coldStartEvidence) coldStartVerdict {
	if len(e.fallbacks) == 0 && e.builds <= e.designedBuilds {
		return won()
	}
	for _, check := range []func(coldStartEvidence) (coldStartVerdict, bool){
		checkLossCounts, checkFallbackCauses, checkLingerExits, checkLingerAccounting, checkConnectWaitTiming,
	} {
		if verdict, done := check(e); done {
			return verdict
		}
	}
	return lost()
}

func checkLossCounts(e coldStartEvidence) (coldStartVerdict, bool) {
	if e.spawns < e.builds {
		return failed("%d builds from only %d spawns", e.builds, e.spawns), true
	}
	if len(e.fallbacks) > e.launches {
		return failed("%d fallback warnings from %d launches", len(e.fallbacks), e.launches), true
	}
	return coldStartVerdict{}, false
}

func checkFallbackCauses(e coldStartEvidence) (coldStartVerdict, bool) {
	for _, f := range e.fallbacks {
		if f.cause != fallbackCauseDroppedTwice && f.cause != fallbackCauseConnectWait {
			return failed("a fallback with cause %q is not a scheduling loss", f.cause), true
		}
	}
	return coldStartVerdict{}, false
}

func checkLingerExits(e coldStartEvidence) (coldStartVerdict, bool) {
	for _, exit := range e.exits {
		if exit.Err != nil {
			return failed("a built candidate exited with an error: %v", exit.Err), true
		}
		if lived := exit.ExitedAt.Sub(exit.SpawnedAt); lived < config.MinSharedServerLinger {
			return failed("a built candidate exited %v after its spawn, before the linger elapsed", lived), true
		}
	}
	return coldStartVerdict{}, false
}

func checkLingerAccounting(e coldStartEvidence) (coldStartVerdict, bool) {
	extraBuilds := max(e.builds-e.designedBuilds, 0)
	droppedTwice, connectWait := countCauses(e.fallbacks)
	lingeredOut := len(e.exits)
	switch {
	case extraBuilds < droppedTwice:
		return failed("%d dropped_twice fallbacks but only %d builds beyond design", droppedTwice, extraBuilds), true
	case lingeredOut < extraBuilds:
		return failed("%d builds beyond design but only %d candidates lingered out", extraBuilds, lingeredOut), true
	case lingeredOut < connectWait:
		return failed("%d connect_wait fallbacks but only %d candidates lingered out", connectWait, lingeredOut), true
	case lingeredOut > extraBuilds+droppedTwice+connectWait:
		return failed("%d candidates lingered out, more than the %d lost ones", lingeredOut, extraBuilds+droppedTwice+connectWait), true
	case allLaunchesFellBack(e) && lingeredOut != e.builds:
		return failed("every launch fell back, yet %d of %d built candidates are still serving", e.builds-lingeredOut, e.builds), true
	}
	return coldStartVerdict{}, false
}

// checkConnectWaitTiming requires each connect_wait fallback to follow the
// linger exit of a distinct candidate and no built candidate to be serving
// when it was written.
func checkConnectWaitTiming(e coldStartEvidence) (coldStartVerdict, bool) {
	exitTimes := make([]time.Time, 0, len(e.exits))
	for _, exit := range e.exits {
		exitTimes = append(exitTimes, exit.ExitedAt)
	}
	slices.SortFunc(exitTimes, time.Time.Compare)
	matched := 0
	for _, at := range connectWaitTimes(e.fallbacks) {
		for _, exit := range e.exits {
			if !exit.SpawnedAt.After(at) && exit.ExitedAt.After(at) {
				return failed("a connect_wait fallback at %v while a built candidate was serving", at.Format(time.StampMilli)), true
			}
		}
		if matched >= len(exitTimes) || exitTimes[matched].After(at) {
			return failed("a connect_wait fallback at %v follows no unmatched linger exit", at.Format(time.StampMilli)), true
		}
		matched++
	}
	return coldStartVerdict{}, false
}

func connectWaitTimes(fallbacks []fallbackRecord) []time.Time {
	var times []time.Time
	for _, f := range fallbacks {
		if f.cause == fallbackCauseConnectWait {
			times = append(times, f.at)
		}
	}
	slices.SortFunc(times, time.Time.Compare)
	return times
}

func countCauses(fallbacks []fallbackRecord) (droppedTwice, connectWait int) {
	for _, f := range fallbacks {
		switch f.cause {
		case fallbackCauseDroppedTwice:
			droppedTwice++
		case fallbackCauseConnectWait:
			connectWait++
		}
	}
	return droppedTwice, connectWait
}

func allLaunchesFellBack(e coldStartEvidence) bool {
	return len(e.fallbacks) > 0 && len(e.fallbacks) == e.launches
}

// builtExitsWanted is how many built candidates a lost attempt must have
// seen exit before it is classified: once every launch has fallen back no
// session holds any of them, so all of them linger out.
func builtExitsWanted(e coldStartEvidence) int {
	if allLaunchesFellBack(e) {
		return e.builds
	}
	_, connectWait := countCauses(e.fallbacks)
	return max(e.builds-e.designedBuilds, connectWait, 0)
}

func collectColdStartEvidence(t *testing.T, h *testkit.SharedServerHarness, designedBuilds int, deadline time.Time) coldStartEvidence {
	t.Helper()
	launches := h.Launches()
	e := coldStartEvidence{
		designedBuilds: designedBuilds,
		launches:       len(launches),
		builds:         h.Builds(),
		spawns:         h.Spawns(),
		fallbacks:      fallbackRecords(t, launches),
	}
	if len(e.fallbacks) == 0 && e.builds <= designedBuilds {
		return e
	}
	e.exits = awaitBuiltExits(h, deadline, builtExitsWanted(e))
	return e
}

func fallbackRecords(t *testing.T, launches []*testkit.LaunchUnderTest) []fallbackRecord {
	t.Helper()
	var fallbacks []fallbackRecord
	for _, l := range launches {
		records, err := slogtext.Parse([]byte(strings.Join(l.FallbackWarnings(), "")))
		require.NoError(t, err)
		for _, record := range records {
			at, err := time.Parse(time.RFC3339Nano, record["time"])
			require.NoError(t, err, "fallback warning: %v", record)
			fallbacks = append(fallbacks, fallbackRecord{cause: record["cause"], at: at})
		}
	}
	return fallbacks
}

// awaitBuiltExits returns the exits of the built candidates once at least
// want of them have been recorded, or at the deadline. A candidate records
// its exit only after it has closed its listener, so a session proxy can move
// on before the record exists.
func awaitBuiltExits(h *testkit.SharedServerHarness, deadline time.Time, want int) []testkit.SharedServerExit {
	expired := time.NewTimer(time.Until(deadline))
	defer expired.Stop()
	poll := time.NewTicker(lossPollInterval)
	defer poll.Stop()
	for {
		exits := builtExits(h)
		if len(exits) >= want {
			return exits
		}
		select {
		case <-expired.C:
			return builtExits(h)
		case <-poll.C:
		}
	}
}

func builtExits(h *testkit.SharedServerHarness) []testkit.SharedServerExit {
	return slices.DeleteFunc(h.SharedServerExits(), func(e testkit.SharedServerExit) bool { return !e.Built })
}
