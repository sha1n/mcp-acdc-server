package sessionproxy

import (
	"context"
	"fmt"
)

// replayHandshake brings the new shared server session to the state the
// host negotiated (spec section 7.3, steps 2 to 4). The responses are the
// proxy's own and never reach the host; a modern session has nothing to
// replay.
func (s *session) replayHandshake(ctx context.Context, att *attachment) error {
	replay, legacy := s.ledger.replay()
	if !legacy {
		return nil
	}
	answer, err := s.replayRequest(ctx, att, methodInitialize, replay.initialize)
	if err != nil {
		return err
	}
	differs, err := compareInitialize(decodeLine(replay.result), answer)
	if err != nil {
		return err
	}
	if len(differs) > 0 {
		s.cfg.Logger.Warn("Replayed initialize result differs from the one the host saw", "differs", differs)
	}
	if replay.initialized != nil {
		if _, err := runPhase(ctx, s.input.ended, func() (struct{}, error) {
			return struct{}{}, replayLine(att, methodInitialized, replay.initialized)
		}); err != nil {
			return err
		}
	}
	if replay.setLevel != nil {
		_, err = s.replayRequest(ctx, att, methodSetLevel, replay.setLevel)
	}
	return err
}

// replayRequest waits for the response without a limit of its own, like the
// ready line: only the session end or a drop ends the wait.
func (s *session) replayRequest(ctx context.Context, att *attachment, method string, line []byte) (decodedLine, error) {
	key, _, _ := requestOf(decodeLine(line))
	return runPhase(ctx, s.input.ended, func() (decodedLine, error) {
		if err := replayLine(att, method, line); err != nil {
			return decodedLine{}, err
		}
		return s.awaitReplayed(ctx, att, method, key)
	})
}

func replayLine(att *attachment, method string, line []byte) error {
	if _, err := att.conn.Write(line); err != nil {
		return droppedInSequence(method, err)
	}
	return nil
}

// awaitReplayed relays every other server line to the host, as the relay
// would: no host request is outstanding on the new connection, so none of
// them answers the replayed request.
func (s *session) awaitReplayed(ctx context.Context, att *attachment, method string, key requestKey) (decodedLine, error) {
	for {
		raw, err := att.fromServer.ReadBytes('\n')
		if err != nil {
			return decodedLine{}, droppedInSequence(method, err)
		}
		if line := decodeLine(raw); answers(line, key) {
			return line, nil
		}
		if end := sessionEnd(ctx, s.input.ended); end != nil {
			return decodedLine{}, end
		}
		if _, err := s.cfg.Stdout.Write(raw); err != nil {
			return decodedLine{}, fmt.Errorf("failed to relay to the host: %w", err)
		}
	}
}

// answers reads line as a response with the id key, as the shared server's
// go-sdk reads it.
func answers(line decodedLine, key requestKey) bool {
	if !line.object || line.hasMethod {
		return false
	}
	responded, ok := keyOf(line.id)
	return ok && !responded.unmatchable && responded == key
}

func droppedInSequence(method string, err error) error {
	return &discoveryError{cause: causeDroppedInSequence, err: fmt.Errorf("shared server connection lost while replaying %s: %w", method, err)}
}

// announceListChanges tells a legacy host that its lists may have changed
// (spec section 8.6), since the new shared server may serve other content.
func (s *session) announceListChanges(ctx context.Context) error {
	replay, legacy := s.ledger.replay()
	if !legacy {
		return nil
	}
	return s.answerLost(ctx, announcements(replay.result))
}
