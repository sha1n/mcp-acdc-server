//go:build unix

package testkit

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"slices"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/stretchr/testify/require"
)

// heldLinger outlasts the one deadline of HoldSharedServer, so the held
// shared server cannot linger out before its session attaches and any exit
// before then is a failure rather than a lost cold start.
const heldLinger = 2 * SharedServerDeadline

// HoldSharedServer starts the shared server of a launch with args once,
// through the spawn seam, and attaches a raw session to it, which cancels its
// initial linger, so a launch started afterwards with args cannot lose its
// cold start (#196). The server runs with heldLinger, which is not part of
// the identity, so that launch still finds it. The test fails if the spawn
// fails, if the server exits before the session has its ready line, or if
// SharedServerDeadline passes first. The session is closed when the test ends.
func (h *SharedServerHarness) HoldSharedServer(args []string) (id string, held *RawSession) {
	h.t.Helper()
	id, held, err := h.holdSharedServer(args, time.Now().Add(SharedServerDeadline))
	require.NoError(h.t, err)
	return id, held
}

func (h *SharedServerHarness) holdSharedServer(args []string, deadline time.Time) (string, *RawSession, error) {
	id := h.Identity(args)
	heldArgs := append(slices.Clone(args), "--shared-server-linger", heldLinger.String())
	if heldID := h.Identity(heldArgs); heldID != id {
		return "", nil, fmt.Errorf("a linger of %s changes the identity from %s to %s", heldLinger, id, heldID)
	}
	socketPath, err := h.Endpoint().SocketPath(id)
	if err != nil {
		return "", nil, err
	}
	exited := len(h.SharedServerExits())
	if err := h.SpawnSharedServer(heldArgs)(id); err != nil {
		return "", nil, fmt.Errorf("failed to spawn the held shared server: %w", err)
	}
	for time.Now().Before(deadline) {
		if exits := h.SharedServerExits(); len(exits) > exited {
			return "", nil, fmt.Errorf("the held shared server ended before a session held it: %+v", exits[exited])
		}
		if held := h.tryAttach(socketPath, deadline); held != nil {
			return id, held, nil
		}
		time.Sleep(pollInterval)
	}
	return "", nil, fmt.Errorf("no session held the shared server within %s", SharedServerDeadline)
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
