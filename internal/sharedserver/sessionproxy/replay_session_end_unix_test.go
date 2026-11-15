//go:build unix

package sessionproxy_test

import (
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	replayLogA = `{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info","data":"line-a"}}` + "\n"
	replayLogB = `{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info","data":"line-b"}}` + "\n"
)

// recordingHostLines holds the first host write of line-a until the test
// opens it, and records every write of line-a or line-b instead of passing
// it on: nobody reads the host pipe once the session has ended.
type recordingHostLines struct {
	host    io.Writer
	entered chan struct{}
	gate    chan struct{}
	once    sync.Once
	opened  sync.Once
	mu      sync.Mutex
	written []string
}

func recordHostLines() (*recordingHostLines, func(*sessionproxy.Config)) {
	r := &recordingHostLines{entered: make(chan struct{}), gate: make(chan struct{})}
	return r, func(cfg *sessionproxy.Config) {
		r.host = cfg.Stdout
		cfg.Stdout = r
	}
}

func (r *recordingHostLines) Write(p []byte) (int, error) {
	line := string(p)
	if !strings.Contains(line, "line-a") && !strings.Contains(line, "line-b") {
		return r.host.Write(p)
	}
	r.mu.Lock()
	r.written = append(r.written, line)
	r.mu.Unlock()
	if strings.Contains(line, "line-a") {
		r.once.Do(func() {
			close(r.entered)
			<-r.gate
		})
	}
	return len(p), nil
}

// release is idempotent and registered like attachGate.release.
func (r *recordingHostLines) release() { r.opened.Do(func() { close(r.gate) }) }

func (r *recordingHostLines) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.written...)
}

func (r *recordingHostLines) awaitEntered(t *testing.T, deadline <-chan time.Time) {
	t.Helper()
	select {
	case <-r.entered:
	case <-deadline:
		t.Fatal("the session proxy never wrote line-a")
	}
}

// awaitNoGoroutineIn returns once no goroutine is running frame, which is
// the abandoned replay wait finishing.
func awaitNoGoroutineIn(t *testing.T, frame string, deadline <-chan time.Time) {
	t.Helper()
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if !strings.Contains(string(buf[:n]), frame) {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("a goroutine is still in %s", frame)
		default:
			runtime.Gosched()
		}
	}
}

func TestRun_ServerLineReadAfterTheSessionEndedDuringTheReplayIsNotRelayed(t *testing.T) {
	cases := []struct {
		name string
		end  func(t *testing.T, l *legacyOutage)
	}{
		{name: "stdin EOF", end: func(t *testing.T, l *legacyOutage) {
			l.host.write("\n")
			require.NoError(t, l.host.stdin.Close())
		}},
		{name: "cancellation", end: func(_ *testing.T, l *legacyOutage) { l.host.cancel() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deadline := time.After(proxyTestDeadline)
			host, configure := recordHostLines()
			l := startLegacyOutage(t, bothListChanges, configure)
			t.Cleanup(host.release)

			recovered := l.recoverOnto(request("7"))
			require.Equal(t, initializeRequest, recovered.readLine())
			// One write puts line-b in the proxy's read buffer, so the wait
			// reads it without touching the connection closed at the end.
			recovered.write(replayLogA + replayLogB)
			host.awaitEntered(t, deadline)
			tc.end(t, l)
			require.NoError(t, l.host.result())
			host.release()

			awaitNoGoroutineIn(t, "sessionproxy.(*session).awaitReplayed", deadline)
			assert.Equal(t, []string{replayLogA}, host.lines(), "line-b was relayed after the session ended")
		})
	}
}
