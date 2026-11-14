// Package sessionproxy relays one stdio session to the shared server for its
// server identity, discovering or starting that shared server first.
package sessionproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
)

type Config struct {
	EndpointDir string
	// Identity is computed only after host stdin is being held.
	Identity    func() (string, error)
	ConnectWait time.Duration
	// Spawn starts a candidate shared server for the identity. It must not
	// wait for the candidate: dialing is the only way to learn it is ready.
	Spawn  func(identity string) error
	Logger *slog.Logger
	Stdin  io.Reader
	Stdout io.Writer
}

// Run returns nil when the session ends normally, on host stdin EOF or on
// cancellation of ctx, and an error when discovery fails or the shared server
// connection breaks. It never waits on the shared server or on host stdio to
// return, since neither can be interrupted.
func Run(ctx context.Context, cfg Config) error {
	input := readHostInput(cfg.Stdin)
	defer input.stop()
	conn, err := discover(ctx, cfg, input.ended)
	if err != nil {
		return sessionResult(ctx, err)
	}
	defer func() { _ = conn.Close() }()

	fromServer := bufio.NewReader(conn)
	if err := awaitReady(ctx, fromServer, input.ended); err != nil {
		return sessionResult(ctx, err)
	}
	return sessionResult(ctx, relay(ctx, conn, fromServer, input, cfg.Stdout))
}

func sessionResult(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, errSessionEnded) {
		return nil
	}
	return err
}

func discover(ctx context.Context, cfg Config, abort <-chan struct{}) (net.Conn, error) {
	identity, err := computeIdentity(ctx, cfg.Identity, abort)
	if err != nil {
		return nil, err
	}
	ep, err := endpoint.OpenEndpoint(cfg.EndpointDir)
	if err != nil {
		return nil, err
	}
	socketPath, err := ep.SocketPath(identity)
	if err != nil {
		return nil, err
	}
	var dialer net.Dialer
	return discovery{
		dial:        func(ctx context.Context) (net.Conn, error) { return dialer.DialContext(ctx, "unix", socketPath) },
		spawn:       func() error { return cfg.Spawn(identity) },
		clock:       realClock{},
		connectWait: cfg.ConnectWait,
		logger:      cfg.Logger,
	}.connect(ctx, abort)
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
		return "", fmt.Errorf("failed to compute the server identity: %w", err)
	}
	return identity, nil
}

// awaitReady has no limit of its own: the shared server may still be building
// its index, and the host's initialize timeout governs that wait.
func awaitReady(ctx context.Context, fromServer *bufio.Reader, abort <-chan struct{}) error {
	result := make(chan error, 1)
	go func() {
		line, err := fromServer.ReadBytes('\n')
		if err != nil {
			result <- fmt.Errorf("shared server connection dropped before the ready line: %w", err)
			return
		}
		_, err = sharedserver.DecodeReadyLine(line)
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

func relay(ctx context.Context, conn net.Conn, fromServer *bufio.Reader, input *hostInput, stdout io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	toServer := make(chan error, 1)
	toHost := make(chan error, 1)
	go func() { toServer <- input.forwardTo(ctx, conn) }()
	go func() { toHost <- copyLines(stdout, fromServer) }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-toServer:
		return err
	case err := <-toHost:
		return err
	case <-input.ended:
		return finishInput(ctx, conn, toServer, input)
	}
}

// eofWriteLimit bounds delivering the lines read before host stdin ended: a
// shared server that stopped reading must not hold the exit beyond the 500 ms
// of spec sections 5.6 and 6.5.
const eofWriteLimit = 200 * time.Millisecond

// finishInput delivers every line read before host stdin ended, as a
// per-session server reads up to EOF; only a write the shared server does not
// take within eofWriteLimit is cut off.
func finishInput(ctx context.Context, conn net.Conn, toServer <-chan error, input *hostInput) error {
	if err := conn.SetWriteDeadline(time.Now().Add(eofWriteLimit)); err != nil {
		return fmt.Errorf("failed to bound the final relay to the shared server: %w", err)
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-toServer:
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return input.endErr()
		}
		return err
	}
}

// copyLines drops an incomplete trailing line: only complete lines are
// forwarded.
func copyLines(dst io.Writer, src *bufio.Reader) error {
	for {
		line, err := src.ReadBytes('\n')
		if err != nil {
			return fmt.Errorf("shared server connection lost: %w", err)
		}
		if _, err := dst.Write(line); err != nil {
			return fmt.Errorf("failed to relay to the host: %w", err)
		}
	}
}
