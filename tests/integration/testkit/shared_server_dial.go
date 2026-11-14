//go:build unix

package testkit

import (
	"bufio"
	"errors"
	"io"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/stretchr/testify/require"
)

// RawSession is one connection dialed straight to a shared server socket,
// bypassing any session proxy. Its ready line has already been consumed.
type RawSession struct {
	h      *SharedServerHarness
	conn   net.Conn
	reader *bufio.Reader
	Ready  sharedserver.ReadyLine
}

// DialSharedServer connects to the shared server socket of id, consumes and
// decodes its ready line, and closes the connection when the test ends.
func (h *SharedServerHarness) DialSharedServer(id string) *RawSession {
	h.t.Helper()
	socketPath, err := h.Endpoint().SocketPath(id)
	require.NoError(h.t, err)
	conn, err := net.DialTimeout("unix", socketPath, SharedServerDeadline)
	require.NoError(h.t, err)
	s := &RawSession{h: h, conn: conn, reader: bufio.NewReader(conn)}
	h.t.Cleanup(func() {
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			h.t.Errorf("failed to close raw shared server session: %v", err)
		}
	})

	ready, err := sharedserver.DecodeReadyLine(s.ReadLine())
	require.NoError(h.t, err)
	s.Ready = ready
	return s
}

// WriteLine writes one line to the shared server; a newline is appended.
func (s *RawSession) WriteLine(line string) {
	s.h.t.Helper()
	require.NoError(s.h.t, s.conn.SetWriteDeadline(time.Now().Add(SharedServerDeadline)))
	_, err := io.WriteString(s.conn, line+"\n")
	require.NoError(s.h.t, err)
}

// ReadLine returns the next complete line the shared server wrote, including
// its newline.
func (s *RawSession) ReadLine() []byte {
	s.h.t.Helper()
	require.NoError(s.h.t, s.conn.SetReadDeadline(time.Now().Add(SharedServerDeadline)))
	line, err := s.reader.ReadBytes('\n')
	require.NoError(s.h.t, err)
	return line
}

// ListenWithoutReady binds the socket of id as a shared server that never
// becomes ready would: it accepts connections and holds them open without
// writing anything. accepted counts the connections accepted so far.
func (h *SharedServerHarness) ListenWithoutReady(id string) (accepted func() int) {
	h.t.Helper()
	var count atomic.Int32
	h.serveSocket(id, func(net.Conn) { count.Add(1) })
	return func() int { return int(count.Load()) }
}

// ServeReadyWithoutAnswering binds the socket of id as a shared server that
// sends its ready line on every connection and then only reads: received
// returns the complete lines read so far across all connections, in arrival
// order.
func (h *SharedServerHarness) ServeReadyWithoutAnswering(id string) (received func() []string) {
	h.t.Helper()
	var mu sync.Mutex
	var lines []string
	h.serveSocket(id, func(conn net.Conn) {
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
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				reportUnlessConnectionEnded(h, "failed to read from a session", err)
				return
			}
			mu.Lock()
			lines = append(lines, line)
			mu.Unlock()
		}
	})
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(lines)
	}
}

// serveSocket runs handle for every connection accepted at the socket of id,
// as serve does.
func (h *SharedServerHarness) serveSocket(id string, handle func(net.Conn)) {
	h.t.Helper()
	h.serve(h.listenAt(id), handle)
}

func (h *SharedServerHarness) listenAt(id string) net.Listener {
	h.t.Helper()
	socketPath, err := h.Endpoint().SocketPath(id)
	require.NoError(h.t, err)
	listener, err := net.Listen("unix", socketPath)
	require.NoError(h.t, err)
	return listener
}

// serve runs handle in its own goroutine for every connection listener
// accepts. When the test ends, whether or not it has failed and whatever the
// order of the other cleanups, the listener and every accepted connection are
// closed and all handlers have returned.
func (h *SharedServerHarness) serve(listener net.Listener, handle func(net.Conn)) {
	held := &heldConns{}
	var handlers sync.WaitGroup
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			conn, err := listener.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					h.t.Errorf("failed to accept at the test socket: %v", err)
				}
				return
			}
			held.add(conn)
			handlers.Go(func() { handle(conn) })
		}
	}()
	h.t.Cleanup(func() {
		closeTestListener(h, listener)
		<-stopped
		held.closeAll(h)
		handlers.Wait()
	})
}

// reportUnlessConnectionEnded treats only the peer going away or the cleanup
// closing the connection as a normal end of a session.
func reportUnlessConnectionEnded(h *SharedServerHarness, what string, err error) {
	ended := errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
	if !ended {
		h.t.Errorf("%s: %v", what, err)
	}
}

type heldConns struct {
	mu    sync.Mutex
	conns []net.Conn
}

func (c *heldConns) add(conn net.Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conns = append(c.conns, conn)
}

func (c *heldConns) closeAll(h *SharedServerHarness) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, conn := range c.conns {
		// A handler may have closed its connection already.
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			h.t.Errorf("failed to close a held connection: %v", err)
		}
	}
}
