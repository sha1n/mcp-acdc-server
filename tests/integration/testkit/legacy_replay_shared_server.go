//go:build unix

package testkit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
)

// LegacyReplayLogPath is the log path the ready line of a LegacyReplayServer
// reports, so a test can tell its attach lines from those of a real shared
// server.
const LegacyReplayLogPath = "/var/log/acdc-legacy-replay-test.log"

// LegacyReplayConnection scripts one connection of a fake legacy-era shared
// server after its ready line. The fake reads complete lines and records each
// before acting on it: it answers every initialize with Result, any other
// request with an empty result, and ignores notifications.
type LegacyReplayConnection struct {
	// Result is the JSON initialize result object.
	Result string
	// BeforeInitializeAnswer is written in the same write as, and ahead of,
	// the initialize answer; it must consist of complete lines.
	BeforeInitializeAnswer string
	// DropOn names the method whose line the fake closes the connection on,
	// without answering it.
	DropOn string
	// DropAfter names the method after whose answer, or for a notification
	// after whose reading, the fake closes the connection.
	DropAfter string
}

// LegacyReplayServer is a fake shared server bound by ServeLegacyReplay.
type LegacyReplayServer struct {
	h        *SharedServerHarness
	lines    *scriptedLines
	accepted atomic.Int32
	dropped  chan struct{}
}

type legacyReplayLine struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

// ServeLegacyReplay binds the socket of id as a fake shared server whose n-th
// accepted connection follows script[n]; a connection beyond the script
// fails the test. The listener stays bound, so a session proxy that dials
// again is accepted again.
func (h *SharedServerHarness) ServeLegacyReplay(id string, script ...LegacyReplayConnection) *LegacyReplayServer {
	h.t.Helper()
	s := &LegacyReplayServer{h: h, lines: &scriptedLines{}, dropped: make(chan struct{}, len(script))}
	h.serveSocket(id, func(conn net.Conn) {
		n := int(s.accepted.Add(1)) - 1
		if n >= len(script) {
			h.t.Errorf("the legacy replay server accepted connection %d beyond its script of %d", n+1, len(script))
			return
		}
		s.lines.open(n)
		s.follow(conn, n, script[n])
	})
	return s
}

// Received returns, per accepted connection, the complete lines the fake has
// read from it, in arrival order.
func (s *LegacyReplayServer) Received() [][]string { return s.lines.snapshot() }

// Accepted counts the connections accepted so far.
func (s *LegacyReplayServer) Accepted() int { return int(s.accepted.Load()) }

// AwaitDropped returns once the fake has closed one more connection by a
// scripted drop than earlier calls returned for, failing the test if none
// is closed within SharedServerDeadline.
func (s *LegacyReplayServer) AwaitDropped() {
	s.h.t.Helper()
	select {
	case <-s.dropped:
	case <-time.After(SharedServerDeadline):
		s.h.t.Fatalf("the legacy replay server dropped no connection within %s", SharedServerDeadline)
	}
}

func (s *LegacyReplayServer) follow(conn net.Conn, n int, step LegacyReplayConnection) {
	readyLine, err := sharedserver.EncodeReadyLine(sharedserver.NewSessionID(), LegacyReplayLogPath)
	if err != nil {
		s.h.t.Errorf("failed to encode the ready line: %v", err)
		return
	}
	if _, err := conn.Write(readyLine); err != nil {
		reportUnlessConnectionEnded(s.h, "failed to write the ready line", err)
		return
	}
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			reportUnlessConnectionEnded(s.h, "failed to read from a session", err)
			return
		}
		s.lines.add(n, line)
		if !s.handle(conn, line, step) {
			return
		}
	}
}

// handle acts on one line and reports whether the connection stays open.
func (s *LegacyReplayServer) handle(conn net.Conn, line string, step LegacyReplayConnection) bool {
	var msg legacyReplayLine
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		s.h.t.Errorf("the legacy replay server read a line that is not a JSON-RPC message: %q", line)
		return false
	}
	if msg.Method == step.DropOn {
		return s.drop(conn)
	}
	if msg.ID != nil {
		if _, err := io.WriteString(conn, answer(msg, step)); err != nil {
			reportUnlessConnectionEnded(s.h, "failed to answer a session", err)
			return false
		}
	}
	if msg.Method == step.DropAfter {
		return s.drop(conn)
	}
	return true
}

func answer(msg legacyReplayLine, step LegacyReplayConnection) string {
	if msg.Method == "initialize" {
		return step.BeforeInitializeAnswer + fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`+"\n", msg.ID, step.Result)
	}
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{}}`+"\n", msg.ID)
}

func (s *LegacyReplayServer) drop(conn net.Conn) bool {
	if err := conn.Close(); err != nil {
		s.h.t.Errorf("failed to drop a legacy replay connection: %v", err)
	}
	s.dropped <- struct{}{}
	return false
}
