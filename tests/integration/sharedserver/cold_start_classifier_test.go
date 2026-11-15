//go:build unix

package sharedserver

import (
	"errors"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	classifiedIdentity = "aaaa"
	otherIdentity      = "bbbb"
)

func TestClassifyColdStart_AcceptsOnlySchedulingLosses(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return t0.Add(d) }
	linger := config.MinSharedServerLinger
	lingered := func(started time.Duration) serverLife {
		return serverLife{started: at(started), lingerExpired: at(started + linger + 50*time.Millisecond), stopped: at(started + linger + 51*time.Millisecond)}
	}
	stoppedAt := func(started, stopped time.Duration) serverLife {
		return serverLife{started: at(started), lingerExpired: at(stopped - time.Millisecond), stopped: at(stopped)}
	}
	serving := func(started time.Duration) serverLife { return serverLife{started: at(started)} }
	logOf := func(servers ...serverLife) map[string]identityLog {
		return map[string]identityLog{classifiedIdentity: {servers: servers}}
	}
	cleanExits := func(n int) []testkit.SharedServerExit {
		exits := make([]testkit.SharedServerExit, n)
		for i := range exits {
			exits[i].Built = true
		}
		return exits
	}
	fallback := func(cause string, d time.Duration) []fallbackRecord {
		return []fallbackRecord{{identity: classifiedIdentity, cause: cause, at: at(d)}}
	}
	connectWaitAt := func(d time.Duration) []fallbackRecord { return fallback(fallbackCauseConnectWait, d) }
	droppedTwiceAt := func(d time.Duration) []fallbackRecord { return fallback(fallbackCauseDroppedTwice, d) }
	singleLaunch := func(e coldStartEvidence) coldStartEvidence {
		e.designedBuilds, e.launches = 1, 1
		return e
	}
	secondLaunch := func(e coldStartEvidence) coldStartEvidence {
		e.designedBuilds, e.launches = 1, 2
		return e
	}
	const fellBack = 5500 * time.Millisecond

	tests := []struct {
		name        string
		evidence    coldStartEvidence
		want        coldStartOutcome
		wantPending bool
	}{
		{name: "designed build without fallback wins", evidence: singleLaunch(coldStartEvidence{builds: 1, spawns: 1}), want: coldStartWon},
		{name: "two designed builds win", evidence: coldStartEvidence{designedBuilds: 2, launches: 2, builds: 2, spawns: 2}, want: coldStartWon},
		{name: "extra build after a linger exit is lost",
			evidence: singleLaunch(coldStartEvidence{builds: 2, spawns: 2, exits: cleanExits(1), logs: logOf(lingered(0), serving(1200*time.Millisecond))}),
			want:     coldStartLost},
		{name: "dropped twice after two linger exits is lost",
			evidence: singleLaunch(coldStartEvidence{builds: 2, spawns: 2, exits: cleanExits(2),
				logs: logOf(lingered(0), lingered(1200*time.Millisecond)), fallbacks: droppedTwiceAt(2400 * time.Millisecond)}),
			want: coldStartLost},
		{name: "dropped twice whose warning precedes both linger-expiry entries is lost",
			evidence: singleLaunch(coldStartEvidence{builds: 2, spawns: 2, exits: cleanExits(2),
				logs: logOf(lingered(0), lingered(1200*time.Millisecond)), fallbacks: droppedTwiceAt(0)}),
			want: coldStartLost},
		{name: "connect wait after its one candidate lingered out is lost",
			evidence: singleLaunch(coldStartEvidence{builds: 1, spawns: 1, exits: cleanExits(1), logs: logOf(lingered(0)), fallbacks: connectWaitAt(fellBack)}),
			want:     coldStartLost},
		{name: "connect wait whose candidate's exit is recorded only after the warning is lost once recorded",
			evidence: singleLaunch(coldStartEvidence{builds: 1, spawns: 1, exits: cleanExits(1),
				logs: logOf(stoppedAt(0, fellBack-4*time.Second)), fallbacks: connectWaitAt(fellBack)}),
			want: coldStartLost},
		{name: "connect wait whose candidate's exit is not yet recorded awaits it",
			evidence: singleLaunch(coldStartEvidence{builds: 1, spawns: 1, logs: logOf(lingered(0)), fallbacks: connectWaitAt(fellBack)}),
			want:     coldStartFailed, wantPending: true},
		{name: "connect wait one millisecond after the stopped entry is lost",
			evidence: singleLaunch(coldStartEvidence{builds: 1, spawns: 1, exits: cleanExits(1),
				logs: logOf(stoppedAt(0, fellBack-time.Millisecond)), fallbacks: connectWaitAt(fellBack)}),
			want: coldStartLost},
		{name: "connect wait of a first launch followed by a second launch's build is lost",
			evidence: secondLaunch(coldStartEvidence{builds: 2, spawns: 2, exits: cleanExits(1),
				logs: logOf(lingered(0), serving(fellBack+time.Millisecond)), fallbacks: connectWaitAt(fellBack)}),
			want: coldStartLost},
		{name: "connect wait while a server of another identity serves is lost",
			evidence: singleLaunch(coldStartEvidence{builds: 1, spawns: 1, exits: cleanExits(1), fallbacks: connectWaitAt(fellBack),
				logs: map[string]identityLog{classifiedIdentity: {servers: []serverLife{lingered(0)}}, otherIdentity: {servers: []serverLife{serving(0)}}}}),
			want: coldStartLost},

		{name: "connect wait while a healthy server serves another session fails",
			evidence: secondLaunch(coldStartEvidence{builds: 2, spawns: 2, exits: cleanExits(1),
				logs: logOf(lingered(0), serving(1300*time.Millisecond)), fallbacks: connectWaitAt(fellBack)}),
			want: coldStartFailed, wantPending: true},
		{name: "connect wait while a server stopping after it was live fails despite an earlier linger exit",
			evidence: secondLaunch(coldStartEvidence{builds: 2, spawns: 2, exits: cleanExits(2),
				logs: logOf(lingered(0), stoppedAt(1300*time.Millisecond, fellBack+2*time.Second)), fallbacks: connectWaitAt(fellBack)}),
			want: coldStartFailed},
		{name: "connect wait at the millisecond of the stopped entry fails",
			evidence: singleLaunch(coldStartEvidence{builds: 1, spawns: 1, exits: cleanExits(1),
				logs: logOf(stoppedAt(0, fellBack)), fallbacks: connectWaitAt(fellBack)}),
			want: coldStartFailed},
		{name: "connect wait at the millisecond of a startup entry fails",
			evidence: secondLaunch(coldStartEvidence{builds: 2, spawns: 2, exits: cleanExits(1),
				logs: logOf(lingered(0), serving(fellBack)), fallbacks: connectWaitAt(fellBack)}),
			want: coldStartFailed, wantPending: true},
		{name: "connect wait while its candidate was still serving fails",
			evidence: singleLaunch(coldStartEvidence{builds: 1, spawns: 1, exits: cleanExits(1), logs: logOf(lingered(0)), fallbacks: connectWaitAt(100 * time.Millisecond)}),
			want:     coldStartFailed},
		{name: "connect wait whose candidate never stops fails",
			evidence: singleLaunch(coldStartEvidence{builds: 1, spawns: 1, logs: logOf(serving(0)), fallbacks: connectWaitAt(fellBack)}),
			want:     coldStartFailed, wantPending: true},
		{name: "connect wait without a shared server log fails",
			evidence: singleLaunch(coldStartEvidence{builds: 0, spawns: 1, fallbacks: connectWaitAt(fellBack)}),
			want:     coldStartFailed, wantPending: true},
		{name: "connect wait with a log of no shared server fails",
			evidence: singleLaunch(coldStartEvidence{builds: 0, spawns: 1, logs: logOf(), fallbacks: connectWaitAt(fellBack)}),
			want:     coldStartFailed},
		{name: "connect wait with an unusable log fails",
			evidence: singleLaunch(coldStartEvidence{builds: 1, spawns: 1, exits: cleanExits(1), fallbacks: connectWaitAt(fellBack),
				logs: map[string]identityLog{classifiedIdentity: {err: errors.New("unparsable entry")}}}),
			want: coldStartFailed},
		{name: "unparsable fallback warning fails",
			evidence: singleLaunch(coldStartEvidence{builds: 1, spawns: 1, exits: cleanExits(1), logs: logOf(lingered(0)),
				fallbacks: []fallbackRecord{{identity: classifiedIdentity, err: errors.New("bad time")}}}),
			want: coldStartFailed},
		{name: "spawn fallback fails",
			evidence: singleLaunch(coldStartEvidence{builds: 0, spawns: 1, fallbacks: fallback("spawn", 0)}), want: coldStartFailed},
		{name: "endpoint fallback fails", evidence: singleLaunch(coldStartEvidence{fallbacks: fallback("endpoint", 0)}), want: coldStartFailed},
		{name: "identity fallback fails", evidence: singleLaunch(coldStartEvidence{fallbacks: fallback("identity", 0)}), want: coldStartFailed},
		{name: "socket path fallback fails", evidence: singleLaunch(coldStartEvidence{fallbacks: fallback("socket_path", 0)}), want: coldStartFailed},
		{name: "fallback without a cause fails",
			evidence: singleLaunch(coldStartEvidence{builds: 1, spawns: 1, exits: cleanExits(1), logs: logOf(lingered(0)), fallbacks: fallback("", fellBack)}),
			want:     coldStartFailed},
		{name: "candidate exiting with an error fails",
			evidence: singleLaunch(coldStartEvidence{builds: 1, spawns: 1, exits: []testkit.SharedServerExit{{Built: true, Err: errors.New("boom")}},
				logs: logOf(lingered(0)), fallbacks: connectWaitAt(fellBack)}),
			want: coldStartFailed},
		{name: "server lingering out before the linger elapsed fails",
			evidence: singleLaunch(coldStartEvidence{builds: 2, spawns: 2, exits: cleanExits(1),
				logs: logOf(serverLife{started: at(0), lingerExpired: at(linger - time.Millisecond), stopped: at(linger)}, serving(2*time.Second))}),
			want: coldStartFailed},
		{name: "server stopping without its linger expiring fails",
			evidence: singleLaunch(coldStartEvidence{builds: 2, spawns: 2, exits: cleanExits(1),
				logs: logOf(serverLife{started: at(0), stopped: at(2 * time.Second)}, serving(3*time.Second))}),
			want: coldStartFailed},
		{name: "server ending without its stopped entry fails",
			evidence: singleLaunch(coldStartEvidence{builds: 2, spawns: 2, exits: cleanExits(1),
				logs: logOf(serverLife{started: at(0), superseded: true}, serving(3*time.Second))}),
			want: coldStartFailed},
		{name: "dropped twice without a build beyond design fails",
			evidence: singleLaunch(coldStartEvidence{builds: 1, spawns: 1, exits: cleanExits(1), logs: logOf(lingered(0)), fallbacks: droppedTwiceAt(1200 * time.Millisecond)}),
			want:     coldStartFailed},
		{name: "dropped twice with one server still serving fails",
			evidence: singleLaunch(coldStartEvidence{builds: 2, spawns: 2, exits: cleanExits(1),
				logs: logOf(lingered(0), serving(1200*time.Millisecond)), fallbacks: droppedTwiceAt(2400 * time.Millisecond)}),
			want: coldStartFailed, wantPending: true},
		{name: "extra build without a linger exit fails",
			evidence: singleLaunch(coldStartEvidence{builds: 2, spawns: 2, logs: logOf(serving(0))}), want: coldStartFailed, wantPending: true},
		{name: "more linger exits than lost candidates fails",
			evidence: coldStartEvidence{designedBuilds: 2, launches: 2, builds: 3, spawns: 3, exits: cleanExits(2),
				logs: logOf(lingered(0), lingered(1200*time.Millisecond), serving(2400*time.Millisecond))},
			want: coldStartFailed},
		{name: "more builds than spawns fails",
			evidence: singleLaunch(coldStartEvidence{builds: 2, spawns: 1, exits: cleanExits(1), logs: logOf(lingered(0))}), want: coldStartFailed},
		{name: "more fallbacks than launches fails",
			evidence: singleLaunch(coldStartEvidence{builds: 2, spawns: 2, exits: cleanExits(2), logs: logOf(lingered(0), lingered(1200*time.Millisecond)),
				fallbacks: append(droppedTwiceAt(2400*time.Millisecond), droppedTwiceAt(2400*time.Millisecond)...)}),
			want: coldStartFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict := classifyColdStart(tt.evidence)
			assert.Equal(t, tt.want, verdict.outcome, "reason: %s", verdict.reason)
			assert.Equal(t, tt.wantPending, verdict.pending, "reason: %s", verdict.reason)
			if tt.want == coldStartFailed {
				assert.NotEmpty(t, verdict.reason)
			}
		})
	}
}

func TestParseServerLives_ReadsEachServerFromItsOwnEntries(t *testing.T) {
	const log = `time=2026-01-01T00:00:00.000Z level=INFO msg="Starting MCP Acdc shared server" version=1 pid=7
time=2026-01-01T00:00:00.100Z level=INFO msg="Session attached" session=s1
time=2026-01-01T00:00:01.200Z level=INFO msg="Shared server linger expired with no session attached" linger=1s
time=2026-01-01T00:00:01.201Z level=INFO msg="Shared server stopped"
time=2026-01-01T00:00:02.000Z level=INFO msg="Starting MCP Acdc shared server" version=1 pid=7
time=2026-01-01T00:00:02.010Z level=ERROR msg="Identity self-check failed" identity=x
time=2026-01-01T00:00:03.000Z level=INFO msg="Starting MCP Acdc shared server" version=1 pid=7
`
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339Nano, s)
		require.NoError(t, err)
		return v
	}

	servers, err := parseServerLives([]byte(log))

	require.NoError(t, err)
	assert.Equal(t, []serverLife{
		{started: at("2026-01-01T00:00:00Z"), lingerExpired: at("2026-01-01T00:00:01.2Z"), stopped: at("2026-01-01T00:00:01.201Z"), superseded: true},
		{started: at("2026-01-01T00:00:02Z"), superseded: true},
		{started: at("2026-01-01T00:00:03Z")},
	}, servers)
}

func TestParseServerLives_RejectsEntriesThatAreNotEvidence(t *testing.T) {
	const startup = "time=2026-01-01T00:00:00.000Z level=INFO msg=\"Starting MCP Acdc shared server\" pid=7\n"
	tests := []struct {
		name string
		log  string
	}{
		{"unparsable entry", startup + "time=2026-01-01T00:00:01.000Z msg=\"unterminated\n"},
		{"lifecycle entry without a time", startup + "level=INFO msg=\"Shared server stopped\"\n"},
		{"lifecycle entry before any startup", "time=2026-01-01T00:00:01.000Z level=INFO msg=\"Shared server stopped\"\n"},
		{"repeated stopped entry", startup + "time=2026-01-01T00:00:01.000Z msg=\"Shared server stopped\"\ntime=2026-01-01T00:00:01.001Z msg=\"Shared server stopped\"\n"},
		{"linger expiry after the stopped entry", startup + "time=2026-01-01T00:00:01.000Z msg=\"Shared server stopped\"\ntime=2026-01-01T00:00:01.001Z msg=\"Shared server linger expired with no session attached\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseServerLives([]byte(tt.log))
			assert.Error(t, err)
		})
	}
}

func TestReadIdentityLog_MissingFileIsNotEvidence(t *testing.T) {
	log := readIdentityLog(t.TempDir() + "/missing.log")
	assert.Error(t, log.err)
}
