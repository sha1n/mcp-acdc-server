package sessionproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
)

type failureCause string

const (
	causeIdentity          failureCause = "identity"
	causeEndpoint          failureCause = "endpoint"
	causeSocketPath        failureCause = "socket_path"
	causeSpawn             failureCause = "spawn"
	causeConnectWait       failureCause = "connect_wait"
	causeDroppedTwice      failureCause = "dropped_twice"
	causeDroppedInSequence failureCause = "dropped_in_sequence"
	causeProtocolVersion   failureCause = "protocol_version"
)

// connectionSetupError is a classified failure to establish a shared server
// connection or to resynchronize it during a recovery. At the first connect
// nothing was relayed yet, so the session can still be served some other
// way; during a recovery it fails the recovery.
type connectionSetupError struct {
	cause failureCause
	err   error
}

func (e *connectionSetupError) Error() string { return e.err.Error() }

func (e *connectionSetupError) Unwrap() error { return e.err }

var errBrokeBeforeReady = errors.New("shared server connection broke before the ready line")

type attachment struct {
	conn       net.Conn
	fromServer *bufio.Reader
}

// establish connects to the shared server for the identity and consumes its
// ready line. A connection that breaks before the ready line, such as a dial
// that landed in the backlog of a listener closing at linger expiry, gets one
// more complete attempt; nothing was relayed on it, so retrying is safe.
func establish(ctx context.Context, cfg Config, abort <-chan struct{}) (*attachment, error) {
	att, err := attachOnce(ctx, cfg, abort)
	if !errors.Is(err, errBrokeBeforeReady) {
		return att, err
	}
	cfg.Logger.Debug("Discovering the shared server again", "error", err)
	att, err = attachOnce(ctx, cfg, abort)
	if errors.Is(err, errBrokeBeforeReady) {
		return nil, &connectionSetupError{cause: causeDroppedTwice, err: err}
	}
	return att, err
}

func attachOnce(ctx context.Context, cfg Config, abort <-chan struct{}) (*attachment, error) {
	conn, err := discover(ctx, cfg, abort)
	if err != nil {
		return nil, endedOr(ctx, abort, err)
	}
	if err := sendHello(conn); err != nil {
		_ = conn.Close()
		return nil, endedOr(ctx, abort, err)
	}
	fromServer := bufio.NewReader(conn)
	ready, err := awaitReady(ctx, fromServer, abort)
	if err != nil {
		_ = conn.Close()
		return nil, endedOr(ctx, abort, err)
	}
	logAttached(cfg.Logger, ready)
	return &attachment{conn: conn, fromServer: fromServer}, nil
}

func discover(ctx context.Context, cfg Config, abort <-chan struct{}) (net.Conn, error) {
	identity, err := runPhase(ctx, abort, func() (string, error) { return computeIdentity(cfg.Identity) })
	if err != nil {
		return nil, err
	}
	socketPath, err := runPhase(ctx, abort, func() (string, error) { return prepareSocketPath(cfg.EndpointDir, identity) })
	if err != nil {
		return nil, err
	}
	var dialer net.Dialer
	conn, err := discovery{
		dial:        func(ctx context.Context) (net.Conn, error) { return dialer.DialContext(ctx, "unix", socketPath) },
		spawn:       func() error { return cfg.Spawn(identity) },
		clock:       realClock{},
		connectWait: cfg.ConnectWait,
		logger:      cfg.Logger,
	}.connect(ctx, abort)
	if errors.Is(err, ErrConnectWaitExpired) {
		return nil, &connectionSetupError{cause: causeConnectWait, err: err}
	}
	return conn, err
}

func computeIdentity(compute func() (string, error)) (string, error) {
	identity, err := compute()
	if err != nil {
		return "", &connectionSetupError{cause: causeIdentity, err: fmt.Errorf("failed to compute the server identity: %w", err)}
	}
	return identity, nil
}

func prepareSocketPath(endpointDir func() (string, error), identity string) (string, error) {
	dir, err := endpointDir()
	if err != nil {
		return "", &connectionSetupError{cause: causeEndpoint, err: fmt.Errorf("failed to resolve the endpoint directory: %w", err)}
	}
	ep, err := endpoint.OpenEndpoint(dir)
	if err != nil {
		return "", &connectionSetupError{cause: causeEndpoint, err: err}
	}
	socketPath, err := ep.SocketPath(identity)
	if err != nil {
		return "", &connectionSetupError{cause: causeSocketPath, err: err}
	}
	return socketPath, nil
}

// runPhase does not wait for op once the session ends: an identity walk or an
// endpoint directory resolution cannot be interrupted and may block. A result
// that arrives with the session already ended is dropped unclassified.
func runPhase[T any](ctx context.Context, abort <-chan struct{}, op func() (T, error)) (T, error) {
	phase, cancel := untilAborted(ctx, abort)
	defer cancel(nil)
	result := make(chan T, 1)
	err := supervise(phase, func() error {
		value, err := op()
		result <- value
		return err
	})
	var zero T
	if end := sessionEnd(ctx, abort); end != nil {
		return zero, end
	}
	if err != nil {
		return zero, err
	}
	return <-result, nil
}

// sessionEnd reports a session that ended, by cancellation or host EOF, even
// when a phase result raced with the end: the end takes priority.
func sessionEnd(ctx context.Context, abort <-chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-abort:
		return errSessionEnded
	default:
		return nil
	}
}

func endedOr(ctx context.Context, abort <-chan struct{}, err error) error {
	if end := sessionEnd(ctx, abort); end != nil {
		return end
	}
	return err
}

// sendHello speaks first on every connection, the first connect and every
// recovery alike: the shared server reads the hello before its ready line. A
// connection that cannot take it broke before the ready line.
func sendHello(conn net.Conn) error {
	hello, err := sharedserver.EncodeHelloLine(sharedserver.HelloSession)
	if err != nil {
		return err
	}
	if _, err := conn.Write(hello); err != nil {
		return fmt.Errorf("%w: failed to send hello line: %w", errBrokeBeforeReady, err)
	}
	return nil
}

// awaitReady has no limit of its own: the shared server may still be building
// its index, and the host's initialize timeout governs that wait.
func awaitReady(ctx context.Context, fromServer *bufio.Reader, abort <-chan struct{}) (sharedserver.ReadyLine, error) {
	type decoded struct {
		ready sharedserver.ReadyLine
		err   error
	}
	result := make(chan decoded, 1)
	go func() {
		line, err := fromServer.ReadBytes('\n')
		var ready sharedserver.ReadyLine
		if err == nil {
			ready, err = sharedserver.DecodeReadyLine(line)
		}
		if err != nil {
			err = fmt.Errorf("%w: %w", errBrokeBeforeReady, err)
		}
		result <- decoded{ready: ready, err: err}
	}()
	select {
	case r := <-result:
		return r.ready, r.err
	case <-ctx.Done():
		return sharedserver.ReadyLine{}, ctx.Err()
	case <-abort:
		return sharedserver.ReadyLine{}, errSessionEnded
	}
}

func logAttached(logger *slog.Logger, ready sharedserver.ReadyLine) {
	if ready.Log == "" {
		logger.Warn("Attached to the shared server, which has no log file", "session", ready.Session)
		return
	}
	logger.Info("Attached to the shared server", "session", ready.Session, "log", ready.Log)
}
