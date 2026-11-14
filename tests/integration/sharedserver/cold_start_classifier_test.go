//go:build unix

package sharedserver

import (
	"errors"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
)

func TestClassifyColdStart_AcceptsOnlySchedulingLosses(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return t0.Add(d) }
	linger := config.MinSharedServerLinger
	lingerExit := func(spawned time.Duration) testkit.SharedServerExit {
		return testkit.SharedServerExit{Built: true, SpawnedAt: at(spawned), ExitedAt: at(spawned + linger + 100*time.Millisecond)}
	}
	connectWaitAt := func(d time.Duration) fallbackRecord {
		return fallbackRecord{cause: fallbackCauseConnectWait, at: at(d)}
	}
	droppedTwiceAt := func(d time.Duration) fallbackRecord {
		return fallbackRecord{cause: fallbackCauseDroppedTwice, at: at(d)}
	}
	singleLaunch := func(e coldStartEvidence) coldStartEvidence {
		e.designedBuilds, e.launches = 1, 1
		return e
	}

	tests := []struct {
		name     string
		evidence coldStartEvidence
		want     coldStartOutcome
	}{
		{"designed build without fallback wins", singleLaunch(coldStartEvidence{builds: 1, spawns: 1}), coldStartWon},
		{"two designed builds win", coldStartEvidence{designedBuilds: 2, launches: 2, builds: 2, spawns: 2}, coldStartWon},
		{"extra build after a linger exit is lost",
			singleLaunch(coldStartEvidence{builds: 2, spawns: 2, exits: []testkit.SharedServerExit{lingerExit(0)}}), coldStartLost},
		{"dropped twice after two linger exits is lost",
			singleLaunch(coldStartEvidence{builds: 2, spawns: 2,
				exits:     []testkit.SharedServerExit{lingerExit(0), lingerExit(1200 * time.Millisecond)},
				fallbacks: []fallbackRecord{droppedTwiceAt(2400 * time.Millisecond)}}), coldStartLost},
		{"connect wait after its one candidate lingered out is lost",
			singleLaunch(coldStartEvidence{builds: 1, spawns: 1,
				exits:     []testkit.SharedServerExit{lingerExit(0)},
				fallbacks: []fallbackRecord{connectWaitAt(5500 * time.Millisecond)}}), coldStartLost},
		{"connect wait of a first launch followed by a second launch's build is lost",
			coldStartEvidence{designedBuilds: 1, launches: 2, builds: 2, spawns: 2,
				exits:     []testkit.SharedServerExit{lingerExit(0)},
				fallbacks: []fallbackRecord{connectWaitAt(5500 * time.Millisecond)}}, coldStartLost},

		{"spawn fallback fails",
			singleLaunch(coldStartEvidence{builds: 0, spawns: 1, fallbacks: []fallbackRecord{{cause: "spawn", at: at(0)}}}), coldStartFailed},
		{"endpoint fallback fails",
			singleLaunch(coldStartEvidence{fallbacks: []fallbackRecord{{cause: "endpoint", at: at(0)}}}), coldStartFailed},
		{"identity fallback fails",
			singleLaunch(coldStartEvidence{fallbacks: []fallbackRecord{{cause: "identity", at: at(0)}}}), coldStartFailed},
		{"socket path fallback fails",
			singleLaunch(coldStartEvidence{fallbacks: []fallbackRecord{{cause: "socket_path", at: at(0)}}}), coldStartFailed},
		{"fallback without a cause fails",
			singleLaunch(coldStartEvidence{builds: 1, spawns: 1, exits: []testkit.SharedServerExit{lingerExit(0)},
				fallbacks: []fallbackRecord{{at: at(5500 * time.Millisecond)}}}), coldStartFailed},
		{"candidate exiting with an error fails",
			singleLaunch(coldStartEvidence{builds: 1, spawns: 1,
				exits:     []testkit.SharedServerExit{{Built: true, Err: errors.New("boom"), SpawnedAt: at(0), ExitedAt: at(2 * time.Second)}},
				fallbacks: []fallbackRecord{connectWaitAt(5500 * time.Millisecond)}}), coldStartFailed},
		{"candidate exiting before the linger fails",
			singleLaunch(coldStartEvidence{builds: 2, spawns: 2,
				exits: []testkit.SharedServerExit{{Built: true, SpawnedAt: at(0), ExitedAt: at(linger - time.Millisecond)}}}), coldStartFailed},
		{"connect wait while its candidate was still serving fails",
			singleLaunch(coldStartEvidence{builds: 1, spawns: 1,
				exits:     []testkit.SharedServerExit{lingerExit(0)},
				fallbacks: []fallbackRecord{connectWaitAt(100 * time.Millisecond)}}), coldStartFailed},
		{"connect wait whose candidate never exits fails",
			singleLaunch(coldStartEvidence{builds: 1, spawns: 1, fallbacks: []fallbackRecord{connectWaitAt(5500 * time.Millisecond)}}), coldStartFailed},
		{"connect wait without a build fails",
			singleLaunch(coldStartEvidence{builds: 0, spawns: 1, fallbacks: []fallbackRecord{connectWaitAt(5500 * time.Millisecond)}}), coldStartFailed},
		{"connect wait while a second launch's server is serving fails",
			coldStartEvidence{designedBuilds: 1, launches: 2, builds: 1, spawns: 1,
				fallbacks: []fallbackRecord{connectWaitAt(5500 * time.Millisecond)}}, coldStartFailed},
		{"dropped twice without a build beyond design fails",
			singleLaunch(coldStartEvidence{builds: 1, spawns: 1, exits: []testkit.SharedServerExit{lingerExit(0)},
				fallbacks: []fallbackRecord{droppedTwiceAt(1200 * time.Millisecond)}}), coldStartFailed},
		{"dropped twice with a built candidate still serving fails",
			singleLaunch(coldStartEvidence{builds: 2, spawns: 2, exits: []testkit.SharedServerExit{lingerExit(0)},
				fallbacks: []fallbackRecord{droppedTwiceAt(2400 * time.Millisecond)}}), coldStartFailed},
		{"extra build without a linger exit fails",
			singleLaunch(coldStartEvidence{builds: 2, spawns: 2}), coldStartFailed},
		{"more linger exits than lost candidates fails",
			coldStartEvidence{designedBuilds: 2, launches: 2, builds: 3, spawns: 3,
				exits: []testkit.SharedServerExit{lingerExit(0), lingerExit(0)}}, coldStartFailed},
		{"more builds than spawns fails",
			singleLaunch(coldStartEvidence{builds: 2, spawns: 1, exits: []testkit.SharedServerExit{lingerExit(0)}}), coldStartFailed},
		{"more fallbacks than launches fails",
			singleLaunch(coldStartEvidence{builds: 2, spawns: 2, exits: []testkit.SharedServerExit{lingerExit(0), lingerExit(0)},
				fallbacks: []fallbackRecord{droppedTwiceAt(2400 * time.Millisecond), droppedTwiceAt(2400 * time.Millisecond)}}), coldStartFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict := classifyColdStart(tt.evidence)
			assert.Equal(t, tt.want, verdict.outcome, "reason: %s", verdict.reason)
			if tt.want == coldStartFailed {
				assert.NotEmpty(t, verdict.reason)
			}
		})
	}
}
