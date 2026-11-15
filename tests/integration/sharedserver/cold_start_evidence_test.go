//go:build unix

package sharedserver

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
)

// The entries a shared server writes to its identity's log. Only the lock
// holder opens the log, and it releases the lock after its last entry, so the
// file is a sequence of contiguous server lifetimes, each opened by its
// startup entry; a candidate that loses the lock writes nothing.
const (
	serverStartupMessage      = "Starting MCP Acdc shared server"
	serverLingerExpiryMessage = "Shared server linger expired with no session attached"
	serverStoppedMessage      = "Shared server stopped"
)

// serverLife is one shared server as its own log entries report it. Run closes
// the listener before the linger-expiry entry and that entry precedes the
// stopped entry, which Run writes only on an error-free return.
type serverLife struct {
	started       time.Time
	lingerExpired time.Time
	stopped       time.Time
	// superseded marks a lifetime followed by another startup entry: it
	// released the lock, so it is no longer running, whatever it wrote.
	superseded bool
}

func (s serverLife) lingeredOut() bool { return !s.lingerExpired.IsZero() && !s.stopped.IsZero() }

// liveAt reports whether the server may still have been accepting at the
// millisecond-truncated time at. Accepting at the real instant behind at means
// its startup entry precedes it and its stopped entry follows it, so after
// truncation started <= at and stopped >= at; an equal stopped time cannot be
// ordered against at and counts as live.
func (s serverLife) liveAt(at time.Time) bool {
	return !s.started.After(at) && (s.stopped.IsZero() || !s.stopped.Before(at))
}

// identityLog is the parsed log of one identity, or why it is not usable.
type identityLog struct {
	servers []serverLife
	err     error
}

func collectColdStartEvidence(h *testkit.SharedServerHarness, designedBuilds int) coldStartEvidence {
	launches := h.Launches()
	e := coldStartEvidence{
		designedBuilds: designedBuilds,
		launches:       len(launches),
		builds:         h.Builds(),
		spawns:         h.Spawns(),
		fallbacks:      fallbackRecords(launches),
	}
	if len(e.fallbacks) == 0 && e.builds <= designedBuilds {
		return e
	}
	e.exits = builtExits(h)
	e.logs = identityLogs(h.LogDir)
	return e
}

func fallbackRecords(launches []*testkit.LaunchUnderTest) []fallbackRecord {
	var fallbacks []fallbackRecord
	for _, l := range launches {
		records, err := slogtext.Parse([]byte(strings.Join(l.FallbackWarnings(), "")))
		if err != nil {
			fallbacks = append(fallbacks, fallbackRecord{identity: l.SharedServerIdentity(), err: err})
			continue
		}
		for _, record := range records {
			at, err := time.Parse(time.RFC3339Nano, record["time"])
			fallbacks = append(fallbacks, fallbackRecord{identity: l.SharedServerIdentity(), cause: record["cause"], at: at, err: err})
		}
	}
	return fallbacks
}

// identityLogs reads every <id>.log in dir. Rotation needs a log far larger
// than any attempt writes, so a rotated generation is never consulted.
func identityLogs(dir string) map[string]identityLog {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	logs := map[string]identityLog{}
	for _, path := range paths {
		id := strings.TrimSuffix(filepath.Base(path), ".log")
		logs[id] = readIdentityLog(path)
	}
	return logs
}

func readIdentityLog(path string) identityLog {
	data, err := os.ReadFile(path)
	if err != nil {
		return identityLog{err: err}
	}
	servers, err := parseServerLives(completeLines(data))
	return identityLog{servers: servers, err: err}
}

// completeLines drops a trailing entry still being written.
func completeLines(data []byte) []byte {
	return data[:strings.LastIndexByte(string(data), '\n')+1]
}

func parseServerLives(data []byte) ([]serverLife, error) {
	records, err := slogtext.Parse(data)
	if err != nil {
		return nil, err
	}
	var servers []serverLife
	for _, record := range records {
		msg := record["msg"]
		if msg != serverStartupMessage && msg != serverLingerExpiryMessage && msg != serverStoppedMessage {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, record["time"])
		if err != nil {
			return nil, fmt.Errorf("entry %q: %w", msg, err)
		}
		if msg == serverStartupMessage {
			if n := len(servers); n > 0 {
				servers[n-1].superseded = true
			}
			servers = append(servers, serverLife{started: at})
			continue
		}
		if err := recordLifecycle(servers, msg, at); err != nil {
			return nil, err
		}
	}
	return servers, nil
}

func recordLifecycle(servers []serverLife, msg string, at time.Time) error {
	if len(servers) == 0 {
		return fmt.Errorf("entry %q precedes every startup entry", msg)
	}
	current := &servers[len(servers)-1]
	field := &current.stopped
	if msg == serverLingerExpiryMessage {
		field = &current.lingerExpired
	}
	if !field.IsZero() || !current.stopped.IsZero() {
		return fmt.Errorf("entry %q at %s follows the stopped entry or repeats", msg, at.Format(time.StampMilli))
	}
	*field = at
	return nil
}

// awaitColdStartVerdict classifies the attempt once its evidence is complete:
// a log entry or an exit record can be written after the session proxy has
// moved on, so a verdict that awaits evidence is retried until the deadline
// and then reported as the failure it is.
func awaitColdStartVerdict(t *testing.T, h *testkit.SharedServerHarness, designedBuilds int, deadline time.Time) coldStartVerdict {
	t.Helper()
	return awaitVerdict(deadline, func() coldStartVerdict {
		return classifyColdStart(collectColdStartEvidence(h, designedBuilds))
	})
}

func awaitVerdict(deadline time.Time, classify func() coldStartVerdict) coldStartVerdict {
	expired := time.NewTimer(time.Until(deadline))
	defer expired.Stop()
	poll := time.NewTicker(lossPollInterval)
	defer poll.Stop()
	for {
		verdict := classify()
		if !verdict.pending {
			return verdict
		}
		select {
		case <-expired.C:
			verdict.reason += " (still so at the deadline)"
			return verdict
		case <-poll.C:
		}
	}
}

// lingeredOutOnce accepts a loss once a shared server of identity id has
// lingered out and every shared server that stopped exited error-free.
func lingeredOutOnce(h *testkit.SharedServerHarness, id string) coldStartVerdict {
	e := coldStartEvidence{exits: builtExits(h), logs: identityLogs(h.LogDir)}
	for _, check := range []func(coldStartEvidence) (coldStartVerdict, bool){checkLogs, checkServerEnds} {
		if verdict, done := check(e); done {
			return verdict
		}
	}
	for _, s := range e.logs[id].servers {
		if s.lingeredOut() {
			return lost()
		}
	}
	return awaiting("no shared server of identity %s lingered out", id)
}

func builtExits(h *testkit.SharedServerHarness) []testkit.SharedServerExit {
	return slices.DeleteFunc(h.SharedServerExits(), func(e testkit.SharedServerExit) bool { return !e.Built })
}
