//go:build unix

package sessionproxy_test

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	initializeRequest  = `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"host","version":"1"}}}` + "\n"
	initializedNote    = `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}` + "\n"
	resourcesChanged   = `{"jsonrpc":"2.0","method":"notifications/resources/list_changed"}` + "\n"
	promptsChanged     = `{"jsonrpc":"2.0","method":"notifications/prompts/list_changed"}` + "\n"
	serverLogMessage   = `{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info","data":"rebuilt"}}` + "\n"
	bothListChanges    = `{"resources":{"listChanged":true},"prompts":{"listChanged":true},"tools":{"listChanged":true}}`
	mismatchWarning    = "Replayed initialize result differs from the one the host saw"
	legacyProtocol     = "2025-11-25"
	legacyInstructions = "use search"
)

func initializeResult(protocol, capabilities, instructions string) string {
	return `{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":"` + protocol + `","capabilities":` + capabilities +
		`,"instructions":"` + instructions + `","serverInfo":{"name":"acdc","version":"1"}}}` + "\n"
}

func setLevel(id, level string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"method":"logging/setLevel","params":{"level":"` + level + `"}}` + "\n"
}

// legacyOutage is an outageFixture whose host completed the initialize
// handshake on the first shared server, and whose replacement shared
// servers listen on the same socket once the test lets the recovery go.
type legacyOutage struct {
	*outageFixture
	computed    *atomic.Int32
	replacement chan net.Conn
	result      string
}

func startLegacyOutage(t *testing.T, capabilities string, configure func(*sessionproxy.Config)) *legacyOutage {
	t.Helper()
	computed := &atomic.Int32{}
	o := startOutage(t, func(cfg *sessionproxy.Config) {
		compute := cfg.Identity
		cfg.Identity = func() (string, error) {
			computed.Add(1)
			return compute()
		}
		if configure != nil {
			configure(cfg)
		}
	})
	l := &legacyOutage{outageFixture: o, computed: computed, replacement: make(chan net.Conn, 4)}
	if capabilities == "" {
		return l
	}
	l.result = initializeResult(legacyProtocol, capabilities, legacyInstructions)
	o.host.write(initializeRequest)
	require.Equal(t, initializeRequest, o.old.readLine())
	o.old.write(l.result)
	require.Equal(t, l.result, o.host.readLine())
	o.host.write(initializedNote)
	require.Equal(t, initializedNote, o.old.readLine())
	return l
}

// answerOnOld sends line through the first shared server and answers it
// with a success, so the host has seen it succeed.
func (l *legacyOutage) answerOnOld(line, id string) {
	l.t.Helper()
	l.host.write(line)
	require.Equal(l.t, line, l.old.readLine())
	l.old.write(response(id))
	require.Equal(l.t, response(id), l.host.readLine())
}

// recoverOnto crashes the first shared server, proves held lines held, and
// returns the replacement connection after its ready line.
func (l *legacyOutage) recoverOnto(held ...string) *fakeSharedConn {
	l.t.Helper()
	l.crash()
	return l.replace(held...)
}

// replace proves held lines held during the outage and returns the
// replacement connection after its ready line.
func (l *legacyOutage) replace(held ...string) *fakeSharedConn {
	l.t.Helper()
	if len(held) > 0 {
		l.hold(held...)
	}
	listener, err := net.Listen("unix", l.socketPath)
	require.NoError(l.t, err)
	accepting := make(chan struct{})
	l.t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-accepting:
		case <-time.After(proxyTestDeadline):
			l.t.Error("the replacement listener never stopped accepting")
		}
	})
	go func() {
		defer close(accepting)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			l.replacement <- conn
		}
	}()
	l.release(proxyTestIdentity, nil)
	return l.nextReplacement()
}

func (l *legacyOutage) nextReplacement() *fakeSharedConn {
	l.t.Helper()
	select {
	case conn := <-l.replacement:
		l.t.Cleanup(func() { _ = conn.Close() })
		c := &fakeSharedConn{t: l.t, conn: conn, lines: bufio.NewReader(conn)}
		c.expectHello()
		c.sendReadyAs("next")
		return c
	case <-time.After(proxyTestDeadline):
		l.t.Fatal("the session proxy never connected to the replacement")
		return nil
	}
}

// requireFailedWithoutRediscovery pins a failed recovery that answered the
// held request 7 and never computed the identity for another attempt.
func (l *legacyOutage) requireFailedWithoutRediscovery(cause string) {
	l.t.Helper()
	assert.Equal(l.t, crashError("7"), l.host.readLine())
	_ = failedRecoveryError(l.t, l.host.result(), cause)
	assert.Empty(l.t, l.remainingOutput(), "nothing follows the answers to the held requests")
	l.requireNoFailureLine()
	assert.Equal(l.t, int32(2), l.computed.Load(), "the failed recovery discovered no shared server again")
}

func TestRun_LegacyRecoveryReplaysTheHandshakeBeforeTheAnnouncementsAndTheHeldMessages(t *testing.T) {
	l := startLegacyOutage(t, bothListChanges, nil)
	l.answerOnOld(setLevel("1", "debug"), "1")

	recovered := l.recoverOnto(request("7"))

	require.Equal(t, initializeRequest, recovered.readLine(), "the cached initialize bytes are replayed")
	recovered.write(l.result)
	require.Equal(t, initializedNote, recovered.readLine())
	require.Equal(t, setLevel("1", "debug"), recovered.readLine())
	recovered.write(response("1"))
	assert.Equal(t, resourcesChanged, l.host.readLine())
	assert.Equal(t, promptsChanged, l.host.readLine())
	require.Equal(t, request("7"), recovered.readLine(), "held messages follow the announcements")
	recovered.write(response("7"))
	assert.Equal(t, response("7"), l.host.readLine(), "no replayed response reached the host")
	l.host.write(request("999")[1:])
	require.Equal(t, request("999"), recovered.readLine())

	assert.Len(t, l.logs.matching(slog.LevelInfo, recoveredInfo), 1)
	assert.Empty(t, l.logs.matching(slog.LevelWarn, mismatchWarning))
}

func TestRun_LegacyRecoveryForwardsOtherServerLinesDuringTheReplay(t *testing.T) {
	l := startLegacyOutage(t, `{}`, nil)
	l.answerOnOld(setLevel("1", "debug"), "1")

	recovered := l.recoverOnto(request("7"))

	require.Equal(t, initializeRequest, recovered.readLine())
	recovered.write(serverLogMessage)
	assert.Equal(t, serverLogMessage, l.host.readLine())
	recovered.write(l.result)
	require.Equal(t, initializedNote, recovered.readLine())
	require.Equal(t, setLevel("1", "debug"), recovered.readLine())
	recovered.write(response("2") + serverLogMessage + response("1"))
	assert.Equal(t, response("2"), l.host.readLine(), "a response to another id is not the replayed one")
	assert.Equal(t, serverLogMessage, l.host.readLine())
	require.Equal(t, request("7"), recovered.readLine())
	recovered.write(response("7"))
	assert.Equal(t, response("7"), l.host.readLine())
}

func TestRun_LegacyRecoveryDiscardsAnErrorAnswerToTheReplayedSetLevel(t *testing.T) {
	l := startLegacyOutage(t, `{}`, nil)
	l.answerOnOld(setLevel("1", "debug"), "1")

	recovered := l.recoverOnto(request("7"))

	require.Equal(t, initializeRequest, recovered.readLine())
	recovered.write(l.result)
	require.Equal(t, initializedNote, recovered.readLine())
	require.Equal(t, setLevel("1", "debug"), recovered.readLine())
	recovered.write(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"bad level"}}` + "\n")
	require.Equal(t, request("7"), recovered.readLine())
	recovered.write(response("7"))
	assert.Equal(t, response("7"), l.host.readLine())
}

func TestRun_LegacyRecoveryAnnouncesOnlyTheListChangesTheHostSawAdvertised(t *testing.T) {
	cases := []struct {
		name         string
		capabilities string
		want         []string
	}{
		{name: "resources and prompts", capabilities: bothListChanges, want: []string{resourcesChanged, promptsChanged}},
		{name: "resources only", capabilities: `{"resources":{"listChanged":true},"prompts":{},"tools":{"listChanged":true}}`, want: []string{resourcesChanged}},
		{name: "prompts only", capabilities: `{"resources":{"subscribe":true},"prompts":{"listChanged":true}}`, want: []string{promptsChanged}},
		{name: "tools only", capabilities: `{"tools":{"listChanged":true}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := startLegacyOutage(t, tc.capabilities, nil)

			recovered := l.recoverOnto(request("7"))

			require.Equal(t, initializeRequest, recovered.readLine())
			recovered.write(initializeResult(legacyProtocol, bothListChanges, legacyInstructions))
			require.Equal(t, initializedNote, recovered.readLine())
			for _, want := range tc.want {
				assert.Equal(t, want, l.host.readLine())
			}
			require.Equal(t, request("7"), recovered.readLine())
			recovered.write(response("7"))
			assert.Equal(t, response("7"), l.host.readLine(), "nothing else is announced")
		})
	}
}

func TestRun_ModernRecoverySendsNothingOfItsOwn(t *testing.T) {
	l := startLegacyOutage(t, "", nil)
	l.answerOnOld(setLevel("1", "debug"), "1")
	l.host.write(initializedNote)
	require.Equal(t, initializedNote, l.old.readLine())

	recovered := l.recoverOnto(request("7"))

	require.Equal(t, request("7"), recovered.readLine(), "nothing is replayed before the held messages")
	recovered.write(response("7"))
	assert.Equal(t, response("7"), l.host.readLine(), "nothing is announced")
}

func TestRun_LegacyRecoveryWithoutAnInitializeResultReplaysNothing(t *testing.T) {
	l := startLegacyOutage(t, "", nil)
	l.host.write(initializeRequest)
	require.Equal(t, initializeRequest, l.old.readLine())
	require.NoError(t, l.listener.Close())
	l.old.crash()
	assert.Equal(t, crashError("0"), l.host.readLine(), "the initialize was in flight")
	l.awaitRecomputing()

	recovered := l.replace(request("7"))

	require.Equal(t, request("7"), recovered.readLine())
	recovered.write(response("7"))
	assert.Equal(t, response("7"), l.host.readLine())
}

func TestRun_LegacyRecoveryWarnsOnceWhenTheReplayedResultDiffersAndContinues(t *testing.T) {
	l := startLegacyOutage(t, bothListChanges, nil)

	recovered := l.recoverOnto(request("7"))

	require.Equal(t, initializeRequest, recovered.readLine())
	recovered.write(`{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":"2025-11-25","capabilities":{},"instructions":"other","serverInfo":{"name":"acdc","version":"2"}}}` + "\n")
	require.Equal(t, initializedNote, recovered.readLine())
	assert.Equal(t, resourcesChanged, l.host.readLine(), "the capabilities the host first saw stay in effect")
	assert.Equal(t, promptsChanged, l.host.readLine())
	require.Equal(t, request("7"), recovered.readLine())
	recovered.write(response("7"))
	assert.Equal(t, response("7"), l.host.readLine())

	warnings := l.logs.matching(slog.LevelWarn, mismatchWarning)
	require.Len(t, warnings, 1)
	for _, member := range []string{"capabilities", "instructions", "serverInfo"} {
		assert.Contains(t, warnings[0].attrs["differs"], member)
	}
}

func TestRun_LegacyRecoveryFailsOnAnotherProtocolVersion(t *testing.T) {
	cases := []struct {
		name   string
		answer string
	}{
		{name: "different protocolVersion", answer: initializeResult("2025-06-18", bothListChanges, legacyInstructions)},
		{name: "initialize rejected", answer: `{"jsonrpc":"2.0","id":0,"error":{"code":-32602,"message":"unsupported"}}` + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := startLegacyOutage(t, bothListChanges, nil)
			l.answerOnOld(setLevel("1", "debug"), "1")

			recovered := l.recoverOnto(request("7"))

			require.Equal(t, initializeRequest, recovered.readLine())
			recovered.write(tc.answer)
			l.requireFailedWithoutRediscovery("protocol_version")
			recovered.requireClosedByProxy()
		})
	}
}

// gatedLines holds the first host write that contains marker until the test
// opens it, and passes every write on to the host.
type gatedLines struct {
	host    io.Writer
	marker  string
	entered chan struct{}
	gate    chan struct{}
	once    sync.Once
	opened  sync.Once
}

func gateHostLine(marker string) (*gatedLines, func(*sessionproxy.Config)) {
	g := &gatedLines{marker: marker, entered: make(chan struct{}), gate: make(chan struct{})}
	return g, func(cfg *sessionproxy.Config) {
		g.host = cfg.Stdout
		cfg.Stdout = g
	}
}

func (g *gatedLines) Write(p []byte) (int, error) {
	if strings.Contains(string(p), g.marker) {
		held := false
		g.once.Do(func() { held = true })
		if held {
			close(g.entered)
			<-g.gate
		}
	}
	return g.host.Write(p)
}

// release is idempotent and registered like attachGate.release.
func (g *gatedLines) release() { g.opened.Do(func() { close(g.gate) }) }

func (g *gatedLines) awaitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(proxyTestDeadline):
		t.Fatal("the session proxy never wrote the gated line")
	}
}

func TestRun_DropDuringTheLegacyReplayFailsTheRecovery(t *testing.T) {
	cases := []struct {
		name     string
		setLevel bool
		drop     func(t *testing.T, l *legacyOutage, gate *gatedLines, recovered *fakeSharedConn)
	}{
		{name: "before the initialize response", drop: func(t *testing.T, _ *legacyOutage, _ *gatedLines, recovered *fakeSharedConn) {
			require.Equal(t, initializeRequest, recovered.readLine())
			recovered.crash()
		}},
		{name: "writing notifications/initialized", drop: func(t *testing.T, l *legacyOutage, gate *gatedLines, recovered *fakeSharedConn) {
			require.Equal(t, initializeRequest, recovered.readLine())
			recovered.write(serverLogMessage + l.result)
			gate.awaitEntered(t)
			recovered.crash()
			gate.release()
			assert.Equal(t, serverLogMessage, l.host.readLine())
		}},
		{name: "before the setLevel response", setLevel: true, drop: func(t *testing.T, l *legacyOutage, _ *gatedLines, recovered *fakeSharedConn) {
			require.Equal(t, initializeRequest, recovered.readLine())
			recovered.write(l.result)
			require.Equal(t, initializedNote, recovered.readLine())
			require.Equal(t, setLevel("1", "debug"), recovered.readLine())
			recovered.crash()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate, configure := gateHostLine("rebuilt")
			l := startLegacyOutage(t, bothListChanges, configure)
			t.Cleanup(gate.release)
			if tc.setLevel {
				l.answerOnOld(setLevel("1", "debug"), "1")
			}

			recovered := l.recoverOnto(request("7"))
			tc.drop(t, l, gate, recovered)

			l.requireFailedWithoutRediscovery("dropped_in_sequence")
			assert.Len(t, l.logs.matching(slog.LevelInfo, attachedMessage), 2)
		})
	}
}

func TestRun_DropAfterTheLastReplayStepIsANewCrash(t *testing.T) {
	l := startLegacyOutage(t, bothListChanges, nil)
	l.answerOnOld(setLevel("1", "debug"), "1")

	second := l.recoverOnto()
	require.Equal(t, initializeRequest, second.readLine())
	second.write(l.result)
	require.Equal(t, initializedNote, second.readLine())
	require.Equal(t, setLevel("1", "debug"), second.readLine())
	second.write(response("1"))
	second.crash()
	assert.Equal(t, resourcesChanged, l.host.readLine())
	assert.Equal(t, promptsChanged, l.host.readLine())

	third := l.nextReplacement()
	require.Equal(t, initializeRequest, third.readLine(), "the session is recovered again")
	third.write(l.result)
	require.Equal(t, initializedNote, third.readLine())
	require.Equal(t, setLevel("1", "debug"), third.readLine())
	third.write(response("1"))
	for _, want := range []string{resourcesChanged, promptsChanged} {
		assert.Equal(t, want, l.host.readLine())
	}
	l.host.write(request("8"))
	require.Equal(t, request("8"), third.readLine())
	third.write(response("8"))
	assert.Equal(t, response("8"), l.host.readLine())
	assert.Len(t, l.logs.matching(slog.LevelWarn, crashWarning), 2)
}
