//go:build unix

package testkit

import (
	"bufio"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
)

// ReissueDrop scripts a replacement shared server that a recovery attaches
// to and that drops the connection while the session proxy re-issues its
// listen streams.
type ReissueDrop struct {
	// Reads is how many complete lines the replacement consumes after its
	// ready line before it writes Reply. Consuming every line the proxy sent
	// makes the close an end of stream on every platform rather than a reset.
	Reads int
	// Reply is written verbatim once Reads lines were consumed; the
	// connection is closed right after it.
	Reply string
	// Unbind closes the listener, which removes the socket, before the ready
	// line, so a recovery that follows the drop does not reach the
	// replacement again. Otherwise every later connection is accepted,
	// counted and closed at once.
	Unbind bool
}

// ServeReissueThenDrop binds the socket of id as a replacement following d on
// its first accepted connection. accepted counts the connections accepted so
// far; received returns the lines the first connection consumed, each
// recorded before Reply is written.
func (h *SharedServerHarness) ServeReissueThenDrop(id string, d ReissueDrop) (accepted func() int, received func() []string) {
	h.t.Helper()
	listener := h.listenAt(id)
	var count atomic.Int32
	rec := &consumedLines{}
	h.serve(listener, func(conn net.Conn) {
		if count.Add(1) > 1 {
			closeScriptedConnection(h, conn)
			return
		}
		if d.Unbind {
			closeTestListener(h, listener)
		}
		h.dropDuringReissue(conn, d, rec)
	})
	return func() int { return int(count.Load()) }, rec.snapshot
}

func (h *SharedServerHarness) dropDuringReissue(conn net.Conn, d ReissueDrop, rec *consumedLines) {
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
	for range d.Reads {
		line, err := reader.ReadString('\n')
		if err != nil {
			reportUnlessConnectionEnded(h, "failed to read a re-issued line", err)
			return
		}
		rec.add(line)
	}
	if _, err := io.WriteString(conn, d.Reply); err != nil {
		reportUnlessConnectionEnded(h, "failed to write the scripted reply", err)
		return
	}
	closeScriptedConnection(h, conn)
}

func closeScriptedConnection(h *SharedServerHarness, conn net.Conn) {
	if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		h.t.Errorf("failed to close a scripted connection: %v", err)
	}
}

type consumedLines struct {
	mu    sync.Mutex
	lines []string
}

func (c *consumedLines) add(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, line)
}

func (c *consumedLines) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lines...)
}
