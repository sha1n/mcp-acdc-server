//go:build unix

package testkit

import (
	"bufio"
	"errors"
	"net"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/stretchr/testify/require"
)

// HoldSharedServer starts the shared server of a launch with args through the
// spawn seam and attaches a raw session to it, which cancels its initial
// linger, so a launch started afterwards cannot lose its cold start (#196).
// A candidate that lingered out before the session attached is replaced by a
// fresh one once its exit is recorded; everything happens under one
// SharedServerDeadline. The session is closed when the test ends.
func (h *SharedServerHarness) HoldSharedServer(args []string) (id string, held *RawSession) {
	h.t.Helper()
	id = h.Identity(args)
	socketPath, err := h.Endpoint().SocketPath(id)
	require.NoError(h.t, err)
	spawn := h.SpawnSharedServer(args)
	deadline := time.Now().Add(SharedServerDeadline)
	for time.Now().Before(deadline) {
		exited := len(h.SharedServerExits())
		require.NoError(h.t, spawn(id))
		if held = h.attachBeforeExit(socketPath, exited, deadline); held != nil {
			return id, held
		}
	}
	require.FailNow(h.t, "no shared server held a session", "within %s", SharedServerDeadline)
	return "", nil
}

// attachBeforeExit dials socketPath until a session receives its ready line,
// or returns nil once a candidate spawned after exited exits had ended.
func (h *SharedServerHarness) attachBeforeExit(socketPath string, exited int, deadline time.Time) *RawSession {
	for time.Now().Before(deadline) {
		if held := h.tryAttach(socketPath, deadline); held != nil {
			return held
		}
		if len(h.SharedServerExits()) > exited {
			return nil
		}
		time.Sleep(pollInterval)
	}
	return nil
}

func (h *SharedServerHarness) tryAttach(socketPath string, deadline time.Time) *RawSession {
	conn, err := net.DialTimeout("unix", socketPath, time.Until(deadline))
	if err != nil {
		return nil
	}
	s := &RawSession{h: h, conn: conn, reader: bufio.NewReader(conn)}
	if err := conn.SetReadDeadline(deadline); err == nil {
		if line, err := s.reader.ReadBytes('\n'); err == nil {
			if s.Ready, err = sharedserver.DecodeReadyLine(line); err == nil && conn.SetReadDeadline(time.Time{}) == nil {
				h.t.Cleanup(func() {
					if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
						h.t.Errorf("failed to close the held shared server session: %v", err)
					}
				})
				return s
			}
		}
	}
	_ = conn.Close()
	return nil
}
