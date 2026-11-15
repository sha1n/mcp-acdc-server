package sessionproxy

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	listenMethod        = "subscriptions/listen"
	acknowledgedMethod  = "notifications/subscriptions/acknowledged"
	cancelledMethod     = "notifications/cancelled"
	subscriptionIDField = "io.modelcontextprotocol/subscriptionId"
)

// acknowledgement is what the shared server agreed for a listen stream, keyed
// by the subscription id it stamps (the listen request id as go-sdk reads it).
type acknowledgement struct {
	key            requestKey
	subscriptionID json.RawMessage
	resources      bool
	prompts        bool
}

func acknowledgementOf(line decodedLine) (acknowledgement, bool) {
	if !line.methodIs(acknowledgedMethod) {
		return acknowledgement{}, false
	}
	var params struct {
		Meta          map[string]json.RawMessage `json:"_meta"`
		Notifications struct {
			Resources bool `json:"resourcesListChanged"`
			Prompts   bool `json:"promptsListChanged"`
		} `json:"notifications"`
	}
	if json.Unmarshal(line.params, &params) != nil {
		return acknowledgement{}, false
	}
	id := params.Meta[subscriptionIDField]
	key, ok := keyOf(id)
	if !ok || key.unmatchable {
		return acknowledgement{}, false
	}
	return acknowledgement{key: key, subscriptionID: id, resources: params.Notifications.Resources, prompts: params.Notifications.Prompts}, true
}

// reissue is one open listen stream re-sent to the new shared server; ack
// is its acknowledgement there, nil until it arrives.
type reissue struct {
	stream *occurrence
	ack    *acknowledgement
}

// reissueListens is recovery step 5: every open listen stream is re-sent
// verbatim, in send order, and the proxy reads the new shared server until
// each one is acknowledged or ended. Other lines read meanwhile go to the
// host as the relay would forward them.
func (s *session) reissueListens(ctx context.Context, att *attachment) ([]*reissue, error) {
	var reissues []*reissue
	for _, stream := range s.ledger.open() {
		reissues = append(reissues, &reissue{stream: stream})
	}
	if len(reissues) == 0 {
		return nil, nil
	}
	phase, cancel := untilAborted(ctx, s.input.ended)
	defer cancel(nil)
	err := supervise(phase, func() error { return s.reissueAll(phase, att, reissues) })
	if end := sessionEnd(ctx, s.input.ended); end != nil {
		return nil, end
	}
	return reissues, err
}

func (s *session) reissueAll(ctx context.Context, att *attachment, reissues []*reissue) error {
	for _, r := range reissues {
		if _, err := att.conn.Write(r.stream.line); err != nil {
			return droppedDuringReissue(err)
		}
	}
	for s.awaitingAcknowledgement(reissues) {
		line, err := att.fromServer.ReadBytes('\n')
		if err != nil {
			return droppedDuringReissue(err)
		}
		if err := s.applyReissueLine(ctx, reissues, line); err != nil {
			return err
		}
	}
	return nil
}

func droppedDuringReissue(err error) error {
	return &discoveryError{cause: causeDroppedInSequence, err: fmt.Errorf("shared server connection lost during the listen re-issue: %w", err)}
}

func (s *session) awaitingAcknowledgement(reissues []*reissue) bool {
	for _, r := range reissues {
		if r.ack == nil && s.ledger.isOutstanding(r.stream) {
			return true
		}
	}
	return false
}

// applyReissueLine discards the acknowledgement of a re-issue the host
// already saw acknowledged; every other line reaches the host. A re-issue's
// acknowledgement goes to the oldest re-issue still open and unacknowledged
// with its subscription id: one the server ended during the step is no
// candidate.
func (s *session) applyReissueLine(ctx context.Context, reissues []*reissue, raw []byte) error {
	line := decodeLine(raw)
	ack, isAck := acknowledgementOf(line)
	r := s.acknowledgedReissue(reissues, ack, isAck)
	if r != nil && r.stream.acknowledged {
		r.ack = &ack
		return nil
	}
	var effect delivery
	if r != nil {
		effect.target = r.stream
	} else {
		effect = s.ledger.resolveAs(line, ack, isAck)
	}
	if err := s.writeToHost(ctx, [][]byte{raw}); err != nil {
		return err
	}
	if r != nil {
		r.ack = &ack
	}
	s.ledger.deliver(effect)
	return nil
}

func (s *session) acknowledgedReissue(reissues []*reissue, ack acknowledgement, isAck bool) *reissue {
	if !isAck {
		return nil
	}
	for _, r := range reissues {
		if r.ack == nil && r.stream.key == ack.key && s.ledger.isOutstanding(r.stream) {
			return r
		}
	}
	return nil
}

// announceListens is the modern half of recovery step 6: each re-issued
// stream still open is told that what it subscribed to may have changed,
// since the new shared server may serve other content.
func (s *session) announceListens(ctx context.Context, reissues []*reissue) error {
	var lines [][]byte
	for _, r := range reissues {
		if r.ack == nil || !s.ledger.isOutstanding(r.stream) {
			continue
		}
		if r.ack.resources {
			lines = append(lines, listChanged("resources", r.ack.subscriptionID))
		}
		if r.ack.prompts {
			lines = append(lines, listChanged("prompts", r.ack.subscriptionID))
		}
	}
	phase, cancel := untilAborted(ctx, s.input.ended)
	defer cancel(nil)
	err := s.writeToHost(phase, lines)
	if end := sessionEnd(ctx, s.input.ended); end != nil {
		return end
	}
	return err
}

func listChanged(kind string, subscriptionID json.RawMessage) []byte {
	return []byte(`{"jsonrpc":"2.0","method":"notifications/` + kind + `/list_changed","params":{"_meta":{"` +
		subscriptionIDField + `":` + string(subscriptionID) + `}}}` + "\n")
}
