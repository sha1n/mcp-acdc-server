package sessionproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
)

// BrowseConfig describes one request for the Web UI of the shared server for
// an identity. Discovery, spawning and the connect wait are a session's.
type BrowseConfig struct {
	EndpointDir func() (string, error)
	Identity    func() (string, error)
	ConnectWait time.Duration
	// Spawn starts a candidate shared server for the identity without
	// waiting for it.
	Spawn func(identity string) error
	// UIMaxResults is the requester's ui.max_results; it applies only when
	// this request starts the Web UI.
	UIMaxResults int
	Logger       *slog.Logger
}

func (c BrowseConfig) discoveryConfig() Config {
	return Config{
		EndpointDir: c.EndpointDir,
		Identity:    c.Identity,
		ConnectWait: c.ConnectWait,
		Spawn:       c.Spawn,
		Logger:      c.Logger,
	}
}

// Browse finds or starts the shared server for the identity, as a session
// does, and returns its reply to a browse hello: the ready line with the Web
// UI URL. A reply that reports a failure returns that failure, with the
// decoded line. The reply has no time limit of its own, since the shared
// server answers only once its index is built. A connection that closes
// before the reply gets one more complete attempt, as a session's does; a
// shared server whose build failed closes it again.
func Browse(ctx context.Context, cfg BrowseConfig) (sharedserver.ReadyLine, error) {
	hello, err := sharedserver.EncodeBrowseHelloLine(cfg.UIMaxResults)
	if err != nil {
		return sharedserver.ReadyLine{}, err
	}
	ready, err := browseOnce(ctx, cfg, hello)
	if !errors.Is(err, errBrokeBeforeReady) {
		return ready, err
	}
	cfg.Logger.Debug("Discovering the shared server again", "error", err)
	ready, err = browseOnce(ctx, cfg, hello)
	if errors.Is(err, errBrokeBeforeReady) {
		return ready, fmt.Errorf("the shared server closed the connection without returning the Web UI URL; its index build may have failed: %w", err)
	}
	return ready, err
}

func browseOnce(ctx context.Context, cfg BrowseConfig, hello []byte) (sharedserver.ReadyLine, error) {
	conn, err := discover(ctx, cfg.discoveryConfig(), nil)
	if err != nil {
		return sharedserver.ReadyLine{}, err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write(hello); err != nil {
		return sharedserver.ReadyLine{}, fmt.Errorf("%w: failed to send hello line: %w", errBrokeBeforeReady, err)
	}
	line, err := readReplyLine(ctx, bufio.NewReader(conn), nil)
	if err != nil {
		return sharedserver.ReadyLine{}, err
	}
	return sharedserver.DecodeBrowseReadyLine(line)
}
