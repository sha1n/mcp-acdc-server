package sessionproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
)

// connectionLost is a crash: EOF or an error on the shared server connection
// after its ready line.
type connectionLost struct{ err error }

func (e *connectionLost) Error() string { return "shared server connection lost: " + e.err.Error() }

// session relays one attachment at a time and recovers onto a new one after
// each crash, through the same establish as the first connect.
type session struct {
	cfg      Config
	input    *hostInput
	requests *inFlight
}

type relayEnd struct {
	toServer bool
	err      error
}

func (s *session) serve(ctx context.Context, att *attachment) error {
	for {
		crash, err := s.relay(ctx, att)
		_ = att.conn.Close()
		if crash == nil {
			return err
		}
		if att, err = s.recover(ctx, crash); err != nil {
			return err
		}
	}
}

func (s *session) relay(ctx context.Context, att *attachment) (*connectionLost, error) {
	forwarding, stopForwarding := context.WithCancel(ctx)
	defer stopForwarding()
	ends := make(chan relayEnd, 2)
	toServer := trackingWriter{conn: att.conn, requests: s.requests}
	go func() { ends <- relayEnd{toServer: true, err: s.input.forwardTo(forwarding, toServer)} }()
	go func() { ends <- relayEnd{err: s.copyLines(att.fromServer)} }()
	select {
	case <-ctx.Done():
		return nil, nil
	case end := <-ends:
		return s.settle(ctx, att.conn, stopForwarding, end, ends)
	case <-s.input.ended:
		return nil, finishInput(ctx, att.conn, ends, s.input)
	}
}

// settle makes one crash of whichever direction broke first and waits for the
// other to stop: only then is the in-flight set final, and no line of the old
// connection can reach the host after the crash errors.
//
// The forwarder is stopped by cancellation, not by the host writing, and
// holds host input again when it returns.
func (s *session) settle(ctx context.Context, conn net.Conn, stopForwarding context.CancelFunc, first relayEnd, ends <-chan relayEnd) (*connectionLost, error) {
	var crash *connectionLost
	if !errors.As(first.err, &crash) {
		return nil, first.err
	}
	_ = conn.Close()
	stopForwarding()
	select {
	case <-ctx.Done():
		return nil, nil
	case <-s.input.ended:
		return nil, nil
	case second := <-ends:
		if sessionEnd(ctx, s.input.ended) != nil {
			return nil, nil
		}
		var lost *connectionLost
		if second.err == nil || errors.As(second.err, &lost) {
			return crash, nil
		}
		return nil, second.err
	}
}

func (s *session) recover(ctx context.Context, crash *connectionLost) (*attachment, error) {
	lost := s.requests.drain()
	s.cfg.Logger.Warn("Shared server connection lost, recovering", "error", crash, "in_flight", len(lost))
	if err := s.answerLost(ctx, lost); err != nil {
		return nil, err
	}
	att, err := establish(ctx, s.cfg, s.input.ended)
	if err != nil {
		if end := sessionEnd(ctx, s.input.ended); end != nil {
			return nil, end
		}
		return nil, fmt.Errorf("failed to recover the shared server connection: %w", err)
	}
	s.input.onHeldSent(func(held int) {
		s.cfg.Logger.Info("Recovered the shared server connection", "held", held)
	})
	return att, nil
}

// answerLost does not wait for a host that stopped reading once the session
// ends.
func (s *session) answerLost(ctx context.Context, lines [][]byte) error {
	phase, cancel := untilAborted(ctx, s.input.ended)
	defer cancel(nil)
	err := supervise(phase, func() error {
		for _, line := range lines {
			if _, err := s.cfg.Stdout.Write(line); err != nil {
				return fmt.Errorf("failed to relay to the host: %w", err)
			}
		}
		return nil
	})
	if end := sessionEnd(ctx, s.input.ended); end != nil {
		return end
	}
	return err
}

// copyLines drops an incomplete trailing line: only complete lines are
// forwarded. A response clears its request only once the host has it.
func (s *session) copyLines(fromServer *bufio.Reader) error {
	for {
		line, err := fromServer.ReadBytes('\n')
		if err != nil {
			return &connectionLost{err: err}
		}
		if _, err := s.cfg.Stdout.Write(line); err != nil {
			return fmt.Errorf("failed to relay to the host: %w", err)
		}
		s.requests.answered(line)
	}
}

// trackingWriter puts a request in flight before writing it, so a response
// that arrives while the write returns still finds it, and takes it out again
// when the line was not fully written.
type trackingWriter struct {
	conn     net.Conn
	requests *inFlight
}

func (w trackingWriter) Write(line []byte) (int, error) {
	handle := w.requests.track(line)
	n, err := w.conn.Write(line)
	if err != nil {
		w.requests.untrack(handle)
		return n, &connectionLost{err: err}
	}
	return n, nil
}
