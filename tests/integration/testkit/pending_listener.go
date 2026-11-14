//go:build unix

package testkit

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// PendingListener is a bound socket that never accepts, as a shared server
// holds its socket while it shuts down at linger expiry: a dial completes into
// the kernel backlog and waits there for a ready line that never comes.
type PendingListener struct {
	h          *SharedServerHarness
	listener   *net.UnixListener
	socketPath string
}

// ListenWithoutAccepting binds the socket of id and never accepts on it. The
// listener closes when the test ends.
func (h *SharedServerHarness) ListenWithoutAccepting(id string) *PendingListener {
	h.t.Helper()
	socketPath, err := h.Endpoint().SocketPath(id)
	if err != nil {
		h.t.Fatalf("failed to resolve the socket path: %v", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		h.t.Fatalf("failed to bind the test socket: %v", err)
	}
	pl := &PendingListener{h: h, listener: listener.(*net.UnixListener), socketPath: socketPath}
	h.t.Cleanup(func() { closeTestListener(h, pl.listener) })
	return pl
}

// AwaitPendingConnection blocks until a connection is pending in the backlog,
// without accepting it, and fails the test after SharedServerDeadline.
func (pl *PendingListener) AwaitPendingConnection() {
	pl.h.t.Helper()
	rawConn, err := pl.listener.SyscallConn()
	if err != nil {
		pl.h.t.Fatalf("failed to access the test listener: %v", err)
	}
	var pending bool
	controlErr := rawConn.Control(func(fd uintptr) {
		pending, err = awaitReadable(int32(fd), time.Now().Add(SharedServerDeadline))
	})
	if err = errors.Join(controlErr, err); err != nil {
		pl.h.t.Fatalf("failed to await a pending connection: %v", err)
	}
	if !pending {
		pl.h.t.Fatalf("no connection was pending within %v", SharedServerDeadline)
	}
}

// awaitReadable relies on a listening socket being readable exactly when a
// connection is pending. It blocks in poll(2) on the descriptor because the
// RawConn of a listener rejects Read, so the netpoller cannot wait on it.
func awaitReadable(fd int32, deadline time.Time) (bool, error) {
	for {
		fds := []unix.PollFd{{Fd: fd, Events: unix.POLLIN}}
		n, err := unix.Poll(fds, int(max(time.Until(deadline), 0).Milliseconds()))
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case err != nil:
			return false, err
		case n == 0:
			return false, nil
		case fds[0].Revents&unix.POLLIN == 0:
			return false, fmt.Errorf("unexpected poll events %#x on the test listener", fds[0].Revents)
		}
		return true, nil
	}
}

// Shutdown closes the listener and removes its socket, which ends every
// pending connection.
func (pl *PendingListener) Shutdown() {
	pl.h.t.Helper()
	closeTestListener(pl.h, pl.listener)
	_, err := os.Lstat(pl.socketPath)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		pl.h.t.Fatalf("failed to inspect the test socket: %v", err)
	}
	if err := os.Remove(pl.socketPath); err != nil {
		pl.h.t.Fatalf("failed to remove the test socket: %v", err)
	}
}
