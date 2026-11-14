//go:build unix

package sharedserver

import (
	"context"
	"encoding/json"
	"fmt"
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
		verdict := awaitColdStartVerdict(t, h, builds, deadline)
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

// coldStartVerdict is pending when it fails only for evidence that may still
// be written.
type coldStartVerdict struct {
	outcome coldStartOutcome
	reason  string
	pending bool
}

const (
	fallbackCauseDroppedTwice = "dropped_twice"
	fallbackCauseConnectWait  = "connect_wait"
)

// fallbackRecord is one fallback warning a launch wrote to its stderr, at the
// millisecond-truncated time of the warning.
type fallbackRecord struct {
	identity string
	cause    string
	at       time.Time
	err      error
}

// coldStartEvidence is what one attempt observably did. Exits holds only
// built candidates and serves only to tell whether they ended error-free; the
// lifecycle of every shared server comes from its identity's log.
type coldStartEvidence struct {
	designedBuilds int
	launches       int
	builds         int
	spawns         int
	exits          []testkit.SharedServerExit
	fallbacks      []fallbackRecord
	logs           map[string]identityLog
}

func won() coldStartVerdict { return coldStartVerdict{outcome: coldStartWon} }

func lost() coldStartVerdict { return coldStartVerdict{outcome: coldStartLost} }

func failed(format string, args ...any) coldStartVerdict {
	return coldStartVerdict{outcome: coldStartFailed, reason: fmt.Sprintf(format, args...)}
}

func awaiting(format string, args ...any) coldStartVerdict {
	verdict := failed(format, args...)
	verdict.pending = true
	return verdict
}

// classifyColdStart decides whether an attempt won, lost its cold start to
// scheduling alone, or failed. The initial linger starts at bind, so a
// descheduled session proxy can miss its candidate in exactly two ways:
//   - dropped_twice: its connection landed twice in the backlog of a listener
//     closing at linger expiry, so it built at least once beyond design and
//     two shared servers of its identity lingered out;
//   - connect_wait: its discovery was descheduled past the connect wait while
//     its one candidate bound, stayed unattached and lingered out, so no
//     shared server of its identity was live when it fell back.
//
// A loss is accepted only when every piece of evidence is consistent with
// those; any other outcome, including a fallback with any other cause, a
// shared server that ended with an error or before the linger elapsed, or a
// connect_wait fallback while a shared server of its identity was live, fails.
func classifyColdStart(e coldStartEvidence) coldStartVerdict {
	if len(e.fallbacks) == 0 && e.builds <= e.designedBuilds {
		return won()
	}
	for _, check := range []func(coldStartEvidence) (coldStartVerdict, bool){
		checkLossCounts, checkFallbackCauses, checkLogs, checkServerEnds, checkLingerAccounting, checkConnectWaitLiveness, checkDroppedTwiceServers,
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
		if f.err != nil {
			return failed("an unparsable fallback warning: %v", f.err), true
		}
		if f.cause != fallbackCauseDroppedTwice && f.cause != fallbackCauseConnectWait {
			return failed("a fallback with cause %q is not a scheduling loss", f.cause), true
		}
	}
	return coldStartVerdict{}, false
}

func checkLogs(e coldStartEvidence) (coldStartVerdict, bool) {
	for _, f := range e.fallbacks {
		if _, ok := e.logs[f.identity]; !ok {
			return awaiting("a %s fallback of identity %s, which has no shared server log", f.cause, f.identity), true
		}
	}
	for id, log := range e.logs {
		if log.err != nil {
			return failed("the shared server log of identity %s is not usable evidence: %v", id, log.err), true
		}
	}
	return coldStartVerdict{}, false
}

func checkServerEnds(e coldStartEvidence) (coldStartVerdict, bool) {
	for _, exit := range e.exits {
		if exit.Err != nil {
			return failed("a built candidate exited with an error: %v", exit.Err), true
		}
	}
	stopped := 0
	for id, log := range e.logs {
		for _, s := range log.servers {
			if verdict, done := checkServerEnd(id, s); done {
				return verdict, true
			}
			if !s.stopped.IsZero() {
				stopped++
			}
		}
	}
	if len(e.exits) < stopped {
		return awaiting("%d shared servers logged their stop but only %d built candidates' exits are recorded", stopped, len(e.exits)), true
	}
	return coldStartVerdict{}, false
}

func checkServerEnd(id string, s serverLife) (coldStartVerdict, bool) {
	started := s.started.Format(time.StampMilli)
	switch {
	case s.superseded && s.stopped.IsZero():
		return failed("a shared server of identity %s started at %s ended without its stopped entry", id, started), true
	case !s.stopped.IsZero() && s.lingerExpired.IsZero():
		return failed("a shared server of identity %s started at %s stopped without its linger expiring", id, started), true
	case !s.lingerExpired.IsZero() && s.lingerExpired.Sub(s.started)+time.Millisecond <= config.MinSharedServerLinger:
		return failed("a shared server of identity %s lingered out %v after its startup, before the linger elapsed", id, s.lingerExpired.Sub(s.started)), true
	}
	return coldStartVerdict{}, false
}

func checkLingerAccounting(e coldStartEvidence) (coldStartVerdict, bool) {
	extraBuilds := max(e.builds-e.designedBuilds, 0)
	droppedTwice, connectWait := countCauses(e.fallbacks)
	lingeredOut, fellBackServers, fellBackLingeredOut := 0, 0, 0
	fellBack := fallbackIdentities(e.fallbacks)
	for id, log := range e.logs {
		for _, s := range log.servers {
			if s.lingeredOut() {
				lingeredOut++
			}
			if fellBack[id] {
				fellBackServers++
				if s.lingeredOut() {
					fellBackLingeredOut++
				}
			}
		}
	}
	switch {
	case extraBuilds < droppedTwice:
		return failed("%d dropped_twice fallbacks but only %d builds beyond design", droppedTwice, extraBuilds), true
	case lingeredOut > extraBuilds+droppedTwice+connectWait:
		return failed("%d shared servers lingered out, more than the %d lost ones", lingeredOut, extraBuilds+droppedTwice+connectWait), true
	case lingeredOut < extraBuilds:
		return awaiting("%d builds beyond design but only %d shared servers lingered out", extraBuilds, lingeredOut), true
	case allLaunchesFellBack(e) && fellBackLingeredOut != fellBackServers:
		return awaiting("every launch fell back, yet %d of the %d shared servers of their identities have not lingered out",
			fellBackServers-fellBackLingeredOut, fellBackServers), true
	}
	return coldStartVerdict{}, false
}

// checkConnectWaitLiveness requires that no shared server of the identity was
// live at each connect_wait fallback and that each such fallback follows the
// stop of a distinct shared server of its identity that lingered out.
func checkConnectWaitLiveness(e coldStartEvidence) (coldStartVerdict, bool) {
	for id, times := range connectWaitTimes(e.fallbacks) {
		servers := e.logs[id].servers
		for _, at := range times {
			if verdict, done := checkNoneLiveAt(id, servers, at); done {
				return verdict, true
			}
		}
		if verdict, done := matchConnectWaitStops(id, servers, times); done {
			return verdict, true
		}
	}
	return coldStartVerdict{}, false
}

func checkNoneLiveAt(id string, servers []serverLife, at time.Time) (coldStartVerdict, bool) {
	fellBack := at.Format(time.StampMilli)
	for _, s := range servers {
		if !s.liveAt(at) {
			continue
		}
		started := s.started.Format(time.StampMilli)
		if s.stopped.IsZero() && !s.superseded {
			return awaiting("a shared server of identity %s started at %s has not stopped and was live at the connect_wait fallback at %s", id, started, fellBack), true
		}
		return failed("a shared server of identity %s (started %s, stopped %s) was live at the connect_wait fallback at %s",
			id, started, s.stopped.Format(time.StampMilli), fellBack), true
	}
	return coldStartVerdict{}, false
}

func matchConnectWaitStops(id string, servers []serverLife, times []time.Time) (coldStartVerdict, bool) {
	var stops []time.Time
	for _, s := range servers {
		if s.lingeredOut() {
			stops = append(stops, s.stopped)
		}
	}
	slices.SortFunc(stops, time.Time.Compare)
	for matched, at := range times {
		if matched >= len(stops) || !stops[matched].Before(at) {
			return failed("the connect_wait fallback of identity %s at %s follows the stop of no unmatched shared server that lingered out",
				id, at.Format(time.StampMilli)), true
		}
	}
	return coldStartVerdict{}, false
}

// checkDroppedTwiceServers requires two shared servers of the identity of
// each dropped_twice fallback to have lingered out. Run closes the listener
// before its linger-expiry entry, so a drop and its warning can precede both
// of that server's entries: these are not ordered against the warning.
func checkDroppedTwiceServers(e coldStartEvidence) (coldStartVerdict, bool) {
	for _, f := range e.fallbacks {
		if f.cause != fallbackCauseDroppedTwice {
			continue
		}
		lingeredOut := 0
		for _, s := range e.logs[f.identity].servers {
			if s.lingeredOut() {
				lingeredOut++
			}
		}
		if lingeredOut < 2 {
			return awaiting("a dropped_twice fallback of identity %s, of which only %d shared servers lingered out", f.identity, lingeredOut), true
		}
	}
	return coldStartVerdict{}, false
}

func fallbackIdentities(fallbacks []fallbackRecord) map[string]bool {
	ids := map[string]bool{}
	for _, f := range fallbacks {
		ids[f.identity] = true
	}
	return ids
}

func connectWaitTimes(fallbacks []fallbackRecord) map[string][]time.Time {
	times := map[string][]time.Time{}
	for _, f := range fallbacks {
		if f.cause == fallbackCauseConnectWait {
			times[f.identity] = append(times[f.identity], f.at)
		}
	}
	for _, t := range times {
		slices.SortFunc(t, time.Time.Compare)
	}
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
