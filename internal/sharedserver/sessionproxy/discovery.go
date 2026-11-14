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

// connect bounds the whole dial-or-spawn phase by the connect wait: no
// attempt starts at or after its expiry, and a dial or spawn in progress is
// abandoned when it expires, when ctx ends or when abort closes.
func (d discovery) connect(ctx context.Context, abort <-chan struct{}) (net.Conn, error) {
	deadline := d.clock.Now().Add(d.connectWait)
	phase, cancel := untilAborted(ctx, abort)
	defer cancel(nil)
	stopExpiry := d.clock.AfterFunc(d.connectWait, func() { cancel(ErrConnectWaitExpired) })
	defer stopExpiry()

	schedule := newBackoff()
	for {
		if err := d.expired(phase, deadline); err != nil {
			return nil, err
		}
		conn, err := d.dial(phase)
		if err == nil {
			return conn, nil
		}
		if err := d.expired(phase, deadline); err != nil {
			return nil, err
		}
		d.logger.Debug("Shared server not reachable, spawning a candidate", "error", err)
		if err := supervise(phase, d.spawn); err != nil {
			return nil, d.spawnFailure(phase, err)
		}
		if err := d.expired(phase, deadline); err != nil {
			return nil, err
		}
		select {
		case <-phase.Done():
			return nil, context.Cause(phase)
		case <-d.clock.After(min(schedule.next(), deadline.Sub(d.clock.Now()))):
		}
	}
}

func (d discovery) expired(phase context.Context, deadline time.Time) error {
	if phase.Err() != nil {
		return context.Cause(phase)
	}
	if !d.clock.Now().Before(deadline) {
		return ErrConnectWaitExpired
	}
	return nil
}

func (d discovery) spawnFailure(phase context.Context, err error) error {
	if phase.Err() != nil {
		return context.Cause(phase)
	}
	return &discoveryError{cause: causeSpawn, err: fmt.Errorf("failed to spawn a shared server: %w", err)}
}

// untilAborted ends with errSessionEnded as its cause once abort closes.
func untilAborted(ctx context.Context, abort <-chan struct{}) (context.Context, context.CancelCauseFunc) {
	ctx, cancel := context.WithCancelCause(ctx)
	go func() {
		select {
		case <-abort:
			cancel(errSessionEnded)
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// supervise stops waiting for op once ctx ends, since op cannot be
// interrupted; op then finishes on its own and its result is dropped.
func supervise(ctx context.Context, op func() error) error {
	done := make(chan error, 1)
	go func() { done <- op() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
