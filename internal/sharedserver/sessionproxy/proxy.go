// Package sessionproxy relays one stdio session to the shared server for its
// server identity, discovering or starting that shared server first.
package sessionproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
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
}

// Run returns nil when the session ends normally, on host stdin EOF or on
// cancellation of ctx. A shared server connection that breaks after attach is
// recovered onto a shared server for the identity computed again; when that
// recovery fails, Run answers the held requests and returns an error, even if
// ctx ends meanwhile. When discovery fails at first connect it
// returns the result of cfg.Fallback.
// It never waits on the shared server or on host stdio to return, since
// neither can be interrupted.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Fallback == nil {
		return errors.New("session proxy requires a fallback per-session server")
	}
	input := readHostInput(cfg.Stdin)
	defer input.stop()
	att, err := establish(ctx, cfg, input.ended)
	if err != nil {
		return endWithoutAttach(ctx, cfg, err, input)
	}
	s := &session{cfg: cfg, input: input, ledger: newLedger()}
	return sessionResult(ctx, s.serve(ctx, att))
}

// endWithoutAttach checks the session end again at the fallback decision:
// host EOF may be read after discovery returned its failure, and a host that
// ended is never handed to a per-session server.
func endWithoutAttach(ctx context.Context, cfg Config, err error, input *hostInput) error {
	if sessionEnd(ctx, input.ended) != nil {
		return nil
	}
	var failure *discoveryError
	if errors.As(err, &failure) {
		return fallBack(ctx, cfg, failure, input)
	}
	return sessionResult(ctx, err)
}

func fallBack(ctx context.Context, cfg Config, failure *discoveryError, input *hostInput) error {
	cfg.Logger.Warn("Shared server unavailable, continuing as a per-session server",
		"cause", string(failure.cause), "error", failure)
	return cfg.Fallback(ctx, input.handOver())
}

func sessionResult(ctx context.Context, err error) error {
	var failed *recoveryFailure
	if errors.As(err, &failed) {
		return err
	}
	if ctx.Err() != nil || errors.Is(err, errSessionEnded) {
		return nil
	}
	return err
}

// eofWriteLimit bounds delivering the lines read before host stdin ended: a
// shared server that stopped reading must not hold the exit beyond the 500 ms
// of spec sections 5.6 and 6.5.
const eofWriteLimit = 200 * time.Millisecond

// finishInput delivers every line read before host stdin ended, as a
// per-session server reads up to EOF; only a write the shared server does not
// take within eofWriteLimit is cut off. The session has ended, so a broken
// connection is not recovered.
func finishInput(ctx context.Context, conn net.Conn, ends <-chan relayEnd, input *hostInput) error {
	if err := conn.SetWriteDeadline(time.Now().Add(eofWriteLimit)); err != nil {
		return fmt.Errorf("failed to bound the final relay to the shared server: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case end := <-ends:
			if !end.toServer {
				continue
			}
			var lost *connectionLost
			if errors.As(end.err, &lost) {
				return input.endErr()
			}
			return end.err
		}
	}
}
