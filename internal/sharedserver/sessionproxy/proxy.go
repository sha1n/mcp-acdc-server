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
)

type Config struct {
	EndpointDir func() (string, error)
	// Identity is computed only after host stdin is being held.
	Identity    func() (string, error)
	ConnectWait time.Duration
	// Spawn starts a candidate shared server for the identity. It must not
	// wait for the candidate: dialing is the only way to learn it is ready.
	Spawn func(identity string) error
	// Fallback serves the session as a per-session server when no shared
	// server could be reached before anything was relayed. Its input yields
	// the host input held during discovery, then live host input.
	Fallback func(ctx context.Context, input io.ReadCloser) error
	Logger   *slog.Logger
	Stdin    io.Reader
	Stdout   io.Writer
	clock    clock
}

func (cfg Config) clockOrReal() clock {
	if cfg.clock == nil {
		return realClock{}
	}
	return cfg.clock
}

// Run returns nil when the session ends normally, on host stdin EOF or on
// cancellation of ctx, and an error when the shared server connection breaks
// after attach. When discovery fails it returns the result of cfg.Fallback.
// It never waits on the shared server or on host stdio to return, since
// neither can be interrupted.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Fallback == nil {
		return errors.New("session proxy requires a fallback per-session server")
	}
	input := readHostInput(cfg.Stdin)
	defer input.stop()
	return serve(ctx, cfg, input)
}

func serve(ctx context.Context, cfg Config, input *hostInput) error {
	att, err := establish(ctx, cfg, input.ended)
	var failure *discoveryError
	if errors.As(err, &failure) && sessionResult(ctx, err) != nil {
		return fallBack(ctx, cfg, failure, input)
	}
	if err != nil {
		return sessionResult(ctx, err)
	}
	defer func() { _ = att.conn.Close() }()
	return sessionResult(ctx, relay(ctx, att.conn, att.fromServer, input, cfg.Stdout))
}

func fallBack(ctx context.Context, cfg Config, failure *discoveryError, input *hostInput) error {
	cfg.Logger.Warn("Shared server unavailable, continuing as a per-session server",
		"cause", string(failure.cause), "error", failure)
	return cfg.Fallback(ctx, input.handOver())
}

func sessionResult(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, errSessionEnded) {
		return nil
	}
	return err
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
