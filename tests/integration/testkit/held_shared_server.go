//go:build unix

package testkit

import (
	"bufio"
	"context"
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
	end, err := h.spawnCandidate(heldArgs)(id)
	if err != nil {
		return "", nil, fmt.Errorf("failed to spawn the held shared server: %w", err)
	}
	held, err := awaitAttach(socketPath, end, deadline)
	if err != nil {
		return "", nil, err
	}
	h.t.Cleanup(func() {
		if err := held.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			h.t.Errorf("failed to close the held shared server session: %v", err)
		}
	})
	return id, &RawSession{h: h, conn: held.conn, reader: held.reader, Ready: held.ready}, nil
}

// attachedSession is a session with its decoded ready line that no test owns yet.
type attachedSession struct {
	conn   net.Conn
	reader *bufio.Reader
	ready  sharedserver.ReadyLine
}

// awaitAttach has no harness in scope, so the only exit it can observe is that
// of the candidate of end. It cancels the attach attempt in flight when that
// candidate ends, so its exit is reported as itself and promptly rather than
// as a timeout.
func awaitAttach(socketPath string, end *candidateEnd, deadline time.Time) (*attachedSession, error) {
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	go func() {
		select {
		case <-end.ended:
			cancel()
		case <-ctx.Done():
		}
	}()
	poll := time.NewTicker(pollInterval)
	defer poll.Stop()
	for {
		if held := tryAttach(ctx, socketPath); held != nil {
			return held, nil
		}
		select {
		case <-ctx.Done():
			return nil, holdFailure(end)
		case <-poll.C:
		}
	}
}

// holdFailure checks the candidate again because its end and the deadline can
// both have passed, and a recorded exit is the more useful reason.
func holdFailure(end *candidateEnd) error {
	select {
	case <-end.ended:
		return fmt.Errorf("the held shared server ended before a session held it: %+v", end.exit)
	default:
		return fmt.Errorf("no session held the shared server within %s", SharedServerDeadline)
	}
}

func tryAttach(ctx context.Context, socketPath string) *attachedSession {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil
	}
	if err := SendHello(conn); err != nil {
		_ = conn.Close()
		return nil
	}
	return readReadyLine(ctx, conn)
}

// readReadyLine owns conn and closes it when ctx ends, which also ends a read
// of a ready line that is never written. context.AfterFunc runs at once on a
// ctx that is already done, so a cancellation before the read is honoured too.
func readReadyLine(ctx context.Context, conn net.Conn) *attachedSession {
	closeOnEnd := context.AfterFunc(ctx, func() { _ = conn.Close() })
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	var ready sharedserver.ReadyLine
	if err == nil {
		ready, err = sharedserver.DecodeReadyLine(line)
	}
	if err != nil || !closeOnEnd() {
		_ = conn.Close()
		return nil
	}
	return &attachedSession{conn: conn, reader: reader, ready: ready}
}
