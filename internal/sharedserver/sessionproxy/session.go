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
	cfg    Config
	input  *hostInput
	ledger *ledger
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
	toServer := trackingWriter{conn: att.conn, ledger: s.ledger}
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
	lost := s.ledger.drain()
	s.cfg.Logger.Warn("Shared server connection lost, recovering", "error", crash, "in_flight", len(lost))
	if err := s.answerLost(ctx, lost); err != nil {
		return nil, err
	}
	att, err := establish(ctx, s.cfg, s.input.ended)
	if err == nil {
		err = s.resynchronize(ctx, att)
	}
	if err == nil {
		s.input.onHeldSent(func(held int) {
			s.cfg.Logger.Info("Recovered the shared server connection", "held", held)
		})
		return att, nil
	}
	var failure *discoveryError
	if !errors.As(err, &failure) || sessionEnd(ctx, s.input.ended) != nil {
		return nil, endedOr(ctx, s.input.ended, err)
	}
	return nil, s.failRecovery(ctx, failure)
}

// resynchronize is the recovery sequence between the ready line and the held
// messages (spec section 7.3, steps 2 to 6). A step that fails closes the
// connection and returns a classified failure, which fails the recovery
// instead of discovering again.
func (s *session) resynchronize(ctx context.Context, att *attachment) error {
	reissues, err := s.reissueListens(ctx, att)
	if err == nil {
		err = s.announceListens(ctx, reissues)
	}
	if err != nil {
		_ = att.conn.Close()
	}
	return err
}

// recoveryFailure ends the session whatever happens while it is reported:
// unlike a session end, a cancellation does not make it a normal return.
type recoveryFailure struct{ err error }

func (e *recoveryFailure) Error() string { return e.err.Error() }

func (e *recoveryFailure) Unwrap() error { return e.err }

// failRecovery answers every held request, since none of them can reach a
// shared server any more, and drops the other held lines. The host has only
// the context to cut those answers short: every one is owed before the exit.
func (s *session) failRecovery(ctx context.Context, failure *discoveryError) error {
	err := fmt.Errorf("failed to recover the shared server connection (%s): %w", failure.cause, failure)
	if answerErr := s.writeToHost(ctx, crashAnswers(s.input.abandon())); answerErr != nil {
		err = fmt.Errorf("%w; %w", err, answerErr)
	}
	return &recoveryFailure{err: err}
}

// answerLost does not wait for a host that stopped reading once the session
// ends.
func (s *session) answerLost(ctx context.Context, lines [][]byte) error {
	phase, cancel := untilAborted(ctx, s.input.ended)
	defer cancel(nil)
	err := s.writeToHost(phase, lines)
	if end := sessionEnd(ctx, s.input.ended); end != nil {
		return end
	}
	return err
}

// writeToHost starts no write once ctx ends: the write in progress cannot be
// interrupted, but the host's stdout belongs to the caller after the return.
func (s *session) writeToHost(ctx context.Context, lines [][]byte) error {
	return supervise(ctx, func() error {
		for _, line := range lines {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			if _, err := s.cfg.Stdout.Write(line); err != nil {
				return fmt.Errorf("failed to relay to the host: %w", err)
			}
		}
		return nil
	})
}

// copyLines drops an incomplete trailing line: only complete lines are
// forwarded. A line's target is resolved before the host can react to it and
// its effect is committed only once the host has it.
func (s *session) copyLines(fromServer *bufio.Reader) error {
	for {
		line, err := fromServer.ReadBytes('\n')
		if err != nil {
			return &connectionLost{err: err}
		}
		effect := s.ledger.resolve(decodeLine(line))
		if _, err := s.cfg.Stdout.Write(line); err != nil {
			return fmt.Errorf("failed to relay to the host: %w", err)
		}
		s.ledger.deliver(effect)
	}
}

// trackingWriter registers a request before writing it, so a line that
// arrives while the write returns still finds it, and undoes exactly that
// registration when the line was not fully written. A cancellation's target
// is resolved before the write for the same reason.
type trackingWriter struct {
	conn   net.Conn
	ledger *ledger
}

func (w trackingWriter) Write(raw []byte) (int, error) {
	line := decodeLine(raw)
	cancellation := w.ledger.cancellationTarget(line)
	request := w.ledger.register(line)
	n, err := w.conn.Write(raw)
	if err != nil {
		w.ledger.undo(request)
		return n, &connectionLost{err: err}
	}
	w.ledger.cancel(cancellation)
	return n, nil
}
