package sessionproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
)

type failureCause string

const (
	causeIdentity     failureCause = "identity"
	causeEndpoint     failureCause = "endpoint"
	causeSocketPath   failureCause = "socket_path"
	causeSpawn        failureCause = "spawn"
	causeConnectWait  failureCause = "connect_wait"
	causeDroppedTwice failureCause = "dropped_twice"
)

// discoveryError is a failure to reach a shared server before anything was
// relayed, so the session can still be served some other way.
type discoveryError struct {
	cause failureCause
	err   error
}

func (e *discoveryError) Error() string { return e.err.Error() }

func (e *discoveryError) Unwrap() error { return e.err }

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
		return nil, &discoveryError{cause: causeDroppedTwice, err: err}
	}
	return att, err
}

func attachOnce(ctx context.Context, cfg Config, abort <-chan struct{}) (*attachment, error) {
	conn, err := discover(ctx, cfg, abort)
	if err != nil {
		return nil, err
	}
	fromServer := bufio.NewReader(conn)
	if err := awaitReady(ctx, fromServer, abort); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &attachment{conn: conn, fromServer: fromServer}, nil
}

func discover(ctx context.Context, cfg Config, abort <-chan struct{}) (net.Conn, error) {
	identity, err := computeIdentity(ctx, cfg.Identity, abort)
	if err != nil {
		return nil, err
	}
	dir, err := cfg.EndpointDir()
	if err != nil {
		return nil, &discoveryError{cause: causeEndpoint, err: fmt.Errorf("failed to resolve the endpoint directory: %w", err)}
	}
	ep, err := endpoint.OpenEndpoint(dir)
	if err != nil {
		return nil, &discoveryError{cause: causeEndpoint, err: err}
	}
	socketPath, err := ep.SocketPath(identity)
	if err != nil {
		return nil, &discoveryError{cause: causeSocketPath, err: err}
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
		return nil, &discoveryError{cause: causeConnectWait, err: err}
	}
	return conn, err
}

// computeIdentity does not wait for an identity walk once the session ends:
// the walk cannot be interrupted and may cover a large content tree.
func computeIdentity(ctx context.Context, compute func() (string, error), abort <-chan struct{}) (string, error) {
	phase, cancel := untilAborted(ctx, abort)
	defer cancel(nil)
	var identity string
	err := supervise(phase, func() error {
		var err error
		identity, err = compute()
		return err
	})
	if phase.Err() != nil {
		return "", context.Cause(phase)
	}
	if err != nil {
		return "", &discoveryError{cause: causeIdentity, err: fmt.Errorf("failed to compute the server identity: %w", err)}
	}
	return identity, nil
}

// awaitReady has no limit of its own: the shared server may still be building
// its index, and the host's initialize timeout governs that wait.
func awaitReady(ctx context.Context, fromServer *bufio.Reader, abort <-chan struct{}) error {
	result := make(chan error, 1)
	go func() {
		line, err := fromServer.ReadBytes('\n')
		if err == nil {
			_, err = sharedserver.DecodeReadyLine(line)
		}
		if err != nil {
			err = fmt.Errorf("%w: %w", errBrokeBeforeReady, err)
		}
		result <- err
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-abort:
		return errSessionEnded
	}
}
