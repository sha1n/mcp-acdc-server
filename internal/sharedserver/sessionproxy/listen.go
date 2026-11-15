package sessionproxy

import (
	"bytes"
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

const (
	listenMethod        = "subscriptions/listen"
	acknowledgedMethod  = "notifications/subscriptions/acknowledged"
	cancelledMethod     = "notifications/cancelled"
	subscriptionIDField = "io.modelcontextprotocol/subscriptionId"
)

// listenStream is an open subscriptions/listen request: it has no response
// until the stream ends, so it is repaired at a crash instead of failed.
type listenStream struct {
	key  requestKey
	line []byte
	// acknowledged records that the host saw the stream's acknowledgement,
	// so the one of a re-issue must not reach it again.
	acknowledged bool
}

// listenStreams holds the open listen streams, oldest first.
type listenStreams struct {
	mu      sync.Mutex
	entries list.List
}

func newListenStreams() *listenStreams {
	return &listenStreams{}
}

// track records line if it is a listen request with an id. The handle undoes
// it when the line could not be fully written.
func (l *listenStreams) track(line decodedLine) (*list.Element, bool) {
	if !line.methodIs(listenMethod) {
		return nil, false
	}
	key, ok := keyOf(line.id)
	if !ok {
		return nil, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.entries.PushBack(&listenStream{key: key, line: bytes.Clone(line.raw)}), true
}

func (l *listenStreams) untrack(handle *list.Element) {
	if handle == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries.Remove(handle)
}

// sent ends the stream a host cancellation written to the shared server
// targets.
func (l *listenStreams) sent(line decodedLine) {
	if l.empty() || !line.methodIs(cancelledMethod) {
		return
	}
	var params struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(line.params, &params) != nil {
		return
	}
	if key, ok := keyOf(params.RequestID); ok {
		l.remove(key)
	}
}

// delivered applies a server line written to the host: an acknowledgement
// marks its stream, a response ends it.
func (l *listenStreams) delivered(line decodedLine) {
	if l.empty() || !line.object {
		return
	}
	if !line.hasMethod {
		if key, ok := keyOf(line.id); ok {
			l.remove(key)
		}
		return
	}
	if ack, ok := acknowledgementOf(line); ok {
		l.markAcknowledged(ack.key)
	}
}

func (l *listenStreams) open() []*listenStream {
	l.mu.Lock()
	defer l.mu.Unlock()
	var streams []*listenStream
	for e := l.entries.Front(); e != nil; e = e.Next() {
		streams = append(streams, e.Value.(*listenStream))
	}
	return streams
}

func (l *listenStreams) isOpen(stream *listenStream) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for e := l.entries.Front(); e != nil; e = e.Next() {
		if e.Value == stream {
			return true
		}
	}
	return false
}

func (l *listenStreams) empty() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.entries.Len() == 0
}

func (l *listenStreams) remove(key requestKey) {
	if key.unmatchable {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for e := l.entries.Front(); e != nil; e = e.Next() {
		if e.Value.(*listenStream).key == key {
			l.entries.Remove(e)
			return
		}
	}
}

func (l *listenStreams) markAcknowledged(key requestKey) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for e := l.entries.Front(); e != nil; e = e.Next() {
		if stream := e.Value.(*listenStream); stream.key == key && !stream.acknowledged {
			stream.acknowledged = true
			return
		}
	}
}

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
	stream *listenStream
	ack    *acknowledgement
}

// reissueListens is recovery step 5: every open listen stream is re-sent
// verbatim, in send order, and the proxy reads the new shared server until
// each one is acknowledged or ended. Other lines read meanwhile go to the
// host as the relay would forward them.
func (s *session) reissueListens(ctx context.Context, att *attachment) ([]*reissue, error) {
	var reissues []*reissue
	for _, stream := range s.listens.open() {
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
		if r.ack == nil && s.listens.isOpen(r.stream) {
			return true
		}
	}
	return false
}

// applyReissueLine discards the acknowledgement of a re-issue the host
// already saw acknowledged; every other line reaches the host.
func (s *session) applyReissueLine(ctx context.Context, reissues []*reissue, raw []byte) error {
	line := decodeLine(raw)
	if r, ack := acknowledgedReissue(reissues, line); r != nil {
		r.ack = &ack
		if r.stream.acknowledged {
			return nil
		}
	}
	if err := s.writeToHost(ctx, [][]byte{raw}); err != nil {
		return err
	}
	s.requests.answered(line)
	s.listens.delivered(line)
	return nil
}

func acknowledgedReissue(reissues []*reissue, line decodedLine) (*reissue, acknowledgement) {
	ack, ok := acknowledgementOf(line)
	if !ok {
		return nil, acknowledgement{}
	}
	for _, r := range reissues {
		if r.ack == nil && r.stream.key == ack.key {
			return r, ack
		}
	}
	return nil, acknowledgement{}
}

// announceListens is the modern half of recovery step 6: each re-issued
// stream still open is told that what it subscribed to may have changed,
// since the new shared server may serve other content.
func (s *session) announceListens(ctx context.Context, reissues []*reissue) error {
	var lines [][]byte
	for _, r := range reissues {
		if r.ack == nil || !s.listens.isOpen(r.stream) {
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
