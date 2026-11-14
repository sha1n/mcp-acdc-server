package sessionproxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"
)

// ErrConnectWaitExpired reports that no shared server accepted a connection
// within the session's connect wait.
var ErrConnectWaitExpired = errors.New("connect wait expired before a shared server accepted the connection")

// errSessionEnded reports that host stdin ended before the session attached.
var errSessionEnded = errors.New("host input ended")

// discovery is the dial-or-spawn loop. Dial success is its only liveness
// signal: it never looks at the shared server's lock, so a shared server that
// is shutting down while still holding the lock is met with more candidates
// until one of them wins the lock and listens.
type discovery struct {
	dial        func(context.Context) (net.Conn, error)
	spawn       func() error
	clock       clock
	connectWait time.Duration
	logger      *slog.Logger
}

func (d discovery) connect(ctx context.Context, abort <-chan struct{}) (net.Conn, error) {
	deadline := d.clock.Now().Add(d.connectWait)
	schedule := newBackoff()
	for {
		conn, err := d.dial(ctx)
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		d.logger.Debug("Shared server not reachable, spawning a candidate", "error", err)
		if err := d.spawn(); err != nil {
			return nil, fmt.Errorf("failed to spawn a shared server: %w", err)
		}
		remaining := deadline.Sub(d.clock.Now())
		if remaining <= 0 {
			return nil, ErrConnectWaitExpired
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-abort:
			return nil, errSessionEnded
		case <-d.clock.After(min(schedule.next(), remaining)):
		}
	}
}
