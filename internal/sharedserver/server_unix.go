//go:build unix

package sharedserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"

	"github.com/gofrs/flock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
)

// Run serves the shared server for cfg.Identity until ctx is cancelled.
//
// The order is fixed: lock, log, identity self-check, bind, then build while
// already accepting.
// Only the endpoint directory is created or verified before the lock is held,
// and no catalog or index is built before bind, so a dialable socket means
// "one is alive" while the ready line means "it is ready".
func Run(ctx context.Context, cfg Config) (err error) {
	if !endpoint.ValidIdentity(cfg.Identity) {
		return fmt.Errorf("invalid shared server identity %q", cfg.Identity)
	}
	if cfg.Linger < config.MinSharedServerLinger {
		return fmt.Errorf("shared server linger must be at least %s, got: %s", config.MinSharedServerLinger, cfg.Linger)
	}
	if cfg.CurrentIdentity == nil {
		return errors.New("shared server requires a current identity function")
	}
	ep, err := endpoint.OpenEndpoint(cfg.EndpointDir)
	if err != nil {
		return err
	}
	// Checked here although bind derives the path again: an unusable socket
	// path must be rejected before the lock is taken.
	if _, err := ep.SocketPath(cfg.Identity); err != nil {
		return err
	}

	lock, err := acquireLock(ep, cfg.Identity)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()

	logger, logPath, stopLog := startLog(separateLogDir(cfg.LogDir, cfg.EndpointDir), cfg.Identity)
	defer stopLog()
	logger = slog.New(newSessionHandler(logger.Handler()))
	defer func() { logReturn(logger, err) }()
	if cfg.LogStartup != nil {
		cfg.LogStartup(logger)
	}
	if err := checkIdentity(cfg.Identity, cfg.CurrentIdentity); err != nil {
		return err
	}

	listener, err := bind(ep, cfg.Identity)
	if err != nil {
		return err
	}

	// The acceptor owns the listener from here: every return below waits for
	// its stop sequence, which closes the listener.
	sessions := startAccepting(ctx, logger, listener, ep, cfg.Identity, cfg.Linger)
	server, cleanup, err := cfg.Build(sessions.buildContext(), logger)
	if err != nil {
		return sessions.abandon(err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	return sessions.serve(server, cfg.Revalidate, logPath)
}

// describedFailure is an error that names its own log entry, so that the one
// entry logged for a failed return keeps the attributes of its origin.
type describedFailure interface {
	error
	logEntry() (msg string, args []any)
}

// logReturn is the only writer of the entry that ends a run whose log is
// open: an error return gets exactly one Error entry and no stopped entry.
func logReturn(logger *slog.Logger, err error) {
	if err == nil {
		logger.Info("Shared server stopped")
		return
	}
	var described describedFailure
	if errors.As(err, &described) {
		msg, args := described.logEntry()
		logger.Error(msg, args...)
		return
	}
	logger.Error("Shared server failed", "error", err)
}

// identityCheckError carries the returned error unchanged, so its text and
// chain stay those of fmt.Errorf; only the log entry is added.
type identityCheckError struct {
	err             error
	identity        string
	contentIdentity string
	cause           error
}

func (e *identityCheckError) Error() string {
	return e.err.Error()
}

func (e *identityCheckError) Unwrap() error {
	return e.err
}

func (e *identityCheckError) logEntry() (string, []any) {
	if e.cause != nil {
		return "Identity self-check failed", []any{"identity", e.identity, "error", e.cause}
	}
	return "Identity self-check failed", []any{"identity", e.identity, "content_identity", e.contentIdentity}
}

func checkIdentity(identity string, current func() (string, error)) error {
	actual, err := current()
	if err != nil {
		return &identityCheckError{err: fmt.Errorf("%w: %w", ErrIdentityMismatch, err), identity: identity, cause: err}
	}
	if actual != identity {
		return &identityCheckError{err: fmt.Errorf("%w: content identity is %s", ErrIdentityMismatch, actual), identity: identity, contentIdentity: actual}
	}
	return nil
}

func acquireLock(ep endpoint.Endpoint, identity string) (*flock.Flock, error) {
	path, err := ep.LockPath(identity)
	if err != nil {
		return nil, err
	}
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

func bind(ep endpoint.Endpoint, identity string) (*net.UnixListener, error) {
	socketPath, err := ep.SocketPath(identity)
	if err != nil {
		return nil, err
	}
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

func attach(ctx context.Context, logger *slog.Logger, conn *net.UnixConn, server *mcp.Server, admit func(context.Context) bool, logPath string) {
	defer func() { _ = conn.Close() }()
	stopOnCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopOnCancel()

	session := NewSessionID()
	sessionCtx := withSession(ctx, session)
	if admit != nil && !admit(ctx) {
		return
	}
	readyLine, err := EncodeReadyLine(session, logPath)
	if err != nil {
		logger.ErrorContext(sessionCtx, "Failed to attach session", "error", err)
		return
	}
	if _, err := conn.Write(readyLine); err != nil {
		if peerDisconnected(err) || ctx.Err() != nil {
			return
		}
		logger.ErrorContext(sessionCtx, "Failed to send ready line", "error", err)
		return
	}
	logger.InfoContext(sessionCtx, "Session attached")
	defer logger.InfoContext(sessionCtx, "Session detached")

	// The SDK closes both halves; closing conn twice would end every clean
	// session with a "use of closed network connection" error.
	serverSession, err := server.Connect(sessionCtx, &mcp.IOTransport{Reader: io.NopCloser(conn), Writer: conn}, nil)
	if err != nil {
		logger.ErrorContext(sessionCtx, "Failed to connect session", "error", err)
		return
	}
	if err := serverSession.Wait(); err != nil && !peerDisconnected(err) && ctx.Err() == nil {
		logger.WarnContext(sessionCtx, "Session ended with an error", "error", err)
	}
}
