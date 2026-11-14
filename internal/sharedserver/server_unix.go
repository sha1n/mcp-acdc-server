//go:build unix

package sharedserver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"sync"

	"github.com/gofrs/flock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Run serves the shared server for cfg.Identity until ctx is cancelled.
//
// The order is fixed: lock, bind, build, then accept. Nothing is written
// before the lock is held, and no catalog or index is built before bind, so a
// dialable socket means "one is alive" while the ready line means "it is
// ready".
func Run(ctx context.Context, cfg Config) error {
	if !ValidIdentity(cfg.Identity) {
		return fmt.Errorf("invalid shared server identity %q", cfg.Identity)
	}
	if err := EnsureEndpointDir(cfg.EndpointDir); err != nil {
		return err
	}
	socketPath, err := SocketPath(cfg.EndpointDir, cfg.Identity)
	if err != nil {
		return err
	}

	lock, err := acquireLock(LockPath(cfg.EndpointDir, cfg.Identity))
	if err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()

	listener, err := bind(socketPath)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()

	server, cleanup, err := cfg.Build(ctx)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	return serve(ctx, listener, server)
}

func acquireLock(path string) (*flock.Flock, error) {
	lock := flock.New(path)
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLockNotAcquired, err)
	}
	if !locked {
		return nil, ErrLockNotAcquired
	}
	return lock, nil
}

func bind(socketPath string) (*net.UnixListener, error) {
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to remove stale socket: %w", err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("failed to bind shared server socket: %w", err)
	}
	// Only the lock holder may unlink the socket. A shutting-down server that
	// unlinked on close could delete the socket of the successor that already
	// holds the lock and rebound the path.
	listener.SetUnlinkOnClose(false)
	return listener, nil
}

func serve(ctx context.Context, listener *net.UnixListener, server *mcp.Server) error {
	stopAccepting := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stopAccepting()

	var sessions sync.WaitGroup
	defer sessions.Wait()

	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("failed to accept shared server connection: %w", err)
		}
		sessions.Go(func() { attach(ctx, conn, server) })
	}
}

func attach(ctx context.Context, conn *net.UnixConn, server *mcp.Server) {
	defer func() { _ = conn.Close() }()
	stopOnCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopOnCancel()

	session, err := NewSessionID()
	if err != nil {
		slog.Error("Failed to attach session", "error", err)
		return
	}
	readyLine, err := EncodeReadyLine(session, "")
	if err != nil {
		slog.Error("Failed to attach session", "session", session, "error", err)
		return
	}
	if _, err := conn.Write(readyLine); err != nil {
		slog.Error("Failed to send ready line", "session", session, "error", err)
		return
	}

	serverSession, err := server.Connect(ctx, &mcp.IOTransport{Reader: conn, Writer: conn}, nil)
	if err != nil {
		slog.Error("Failed to connect session", "session", session, "error", err)
		return
	}
	_ = serverSession.Wait()
}
