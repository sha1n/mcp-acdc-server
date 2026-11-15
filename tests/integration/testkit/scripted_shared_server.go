//go:build unix

package testkit

import (
	"bufio"
	"io"
	"net"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
)

// ScriptedConnection scripts one connection of a fake shared server after
// its ready line: the fake reads one complete line, writes Reply verbatim,
// and then either closes the connection (HangUp) or keeps reading until the
// session proxy closes its end. Reply may end without a newline.
type ScriptedConnection struct {
	Reply  string
	HangUp bool
}

// ServeScripted binds the socket of id as a fake shared server whose n-th
// accepted connection follows script[n]; a connection beyond the script
// fails the test. received returns, per accepted connection, the complete
// lines the fake has read from it, in arrival order. A line is recorded
// before the reply to it is written, so a host that has seen the reply
// knows the fake consumed the line.
func (h *SharedServerHarness) ServeScripted(id string, script ...ScriptedConnection) (received func() [][]string) {
	h.t.Helper()
	rec := &scriptedLines{}
	var accepted atomic.Int32
	h.serveSocket(id, func(conn net.Conn) {
		n := int(accepted.Add(1)) - 1
		if n >= len(script) {
			h.t.Errorf("the scripted shared server accepted connection %d beyond its script of %d", n+1, len(script))
			return
		}
		rec.open(n)
		h.followScript(conn, script[n], func(line string) { rec.add(n, line) })
	})
	return rec.snapshot
}

func (h *SharedServerHarness) followScript(conn net.Conn, step ScriptedConnection, record func(string)) {
	readyLine, err := sharedserver.EncodeReadyLine(sharedserver.NewSessionID(), "/var/log/acdc-test.log")
	if err != nil {
		h.t.Errorf("failed to encode the ready line: %v", err)
		return
	}
	if _, err := conn.Write(readyLine); err != nil {
		reportUnlessConnectionEnded(h, "failed to write the ready line", err)
		return
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		reportUnlessConnectionEnded(h, "failed to read the scripted request", err)
		return
	}
	record(line)
	if _, err := io.WriteString(conn, step.Reply); err != nil {
		reportUnlessConnectionEnded(h, "failed to write the scripted reply", err)
		return
	}
	if step.HangUp {
		if err := conn.Close(); err != nil {
			h.t.Errorf("failed to hang up a scripted connection: %v", err)
		}
		return
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			reportUnlessConnectionEnded(h, "failed to read from a session", err)
			return
		}
		record(line)
	}
}

type scriptedLines struct {
	mu    sync.Mutex
	lines [][]string
}

func (s *scriptedLines) open(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.lines) <= n {
		s.lines = append(s.lines, nil)
	}
}

func (s *scriptedLines) add(n int, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines[n] = append(s.lines[n], line)
}

func (s *scriptedLines) snapshot() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]string, len(s.lines))
	for i, l := range s.lines {
		out[i] = slices.Clone(l)
	}
	return out
}
