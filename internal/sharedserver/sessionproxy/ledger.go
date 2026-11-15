package sessionproxy

import (
	"bytes"
	"container/list"
	"encoding/json"
	"sync"
)

// requestKind decides what a crash does to an occurrence: an ordinary request
// is answered with the crash error, an open listen stream is re-issued.
type requestKind int

const (
	ordinaryRequest requestKind = iota
	listenRequest
)

// occurrence is one tracked host request written to the shared server
// connection whose response the host has not yet received.
type occurrence struct {
	kind requestKind
	key  requestKey
	id   json.RawMessage
	// handshake is the method of an initialize or logging/setLevel
	// request, which its success puts in effect for the host.
	handshake string
	// line is kept for a listen request, re-issued verbatim, and for a
	// handshake request, replayed verbatim once it is in effect.
	line []byte
	// cancelled takes a listen stream out of the open set, but the
	// occurrence stays outstanding until its ending response consumes it.
	cancelled bool
	// acknowledged records that the host saw the stream's acknowledgement,
	// so the one of a re-issue must not reach it again.
	acknowledged bool
	// element is nil once the occurrence is no longer outstanding, so a
	// commit resolved earlier can never land on another occurrence.
	element *list.Element
}

// ledger holds every outstanding occurrence in send order, of every kind:
// a response, a cancellation or an acknowledgement selects exactly one of
// them, oldest first, as go-sdk matches ids. committed is the handshake the
// host has in effect: it changes under the same lock that ends the
// occurrence whose success put it in effect, so a crash drain never races
// a commit.
type ledger struct {
	mu        sync.Mutex
	entries   list.List
	committed handshakeReplay
}

func newLedger() *ledger {
	return &ledger{}
}

// register records line before it is written if it is a request with an id;
// undo takes exactly that registration back when the write fails. It returns
// nil for any other line, which is forwarded untracked.
func (l *ledger) register(line decodedLine) *occurrence {
	key, id, ok := requestOf(line)
	if !ok {
		return nil
	}
	o := &occurrence{kind: ordinaryRequest, key: key, id: id}
	if line.methodIs(listenMethod) {
		o.kind = listenRequest
		o.line = bytes.Clone(line.raw)
	}
	if line.methodIs(methodInitialize) || line.methodIs(methodSetLevel) {
		o.handshake = line.method
		o.line = bytes.Clone(line.raw)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	o.element = l.entries.PushBack(o)
	return o
}

func (l *ledger) undo(o *occurrence) {
	if o == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.removeLocked(o)
}

// cancellationTarget resolves the occurrence a host cancellation names before
// it is written: once it is, the server may end that occurrence and a later
// lookup would find its successor.
func (l *ledger) cancellationTarget(line decodedLine) *occurrence {
	if !line.methodIs(cancelledMethod) {
		return nil
	}
	var params struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(line.params, &params) != nil {
		return nil
	}
	key, ok := keyOf(params.RequestID)
	if !ok || key.unmatchable {
		return nil
	}
	return l.oldest(func(o *occurrence) bool { return o.key == key && !o.cancelled })
}

// cancel commits a cancellation the shared server received.
func (l *ledger) cancel(o *occurrence) {
	if o == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	o.cancelled = true
}

// delivery is what a server line does to the occurrence it was read for,
// committed only once the host has the line.
type delivery struct {
	target *occurrence
	ends   bool
	// success is the line of a response carrying a result.
	success []byte
}

func (l *ledger) resolve(line decodedLine) delivery {
	ack, isAck := acknowledgementOf(line)
	return l.resolveAs(line, ack, isAck)
}

// resolveAs is resolve for a line whose acknowledgement the caller has
// already read: an acknowledgement marks its stream, a response ends the
// occurrence it answers.
func (l *ledger) resolveAs(line decodedLine, ack acknowledgement, isAck bool) delivery {
	if isAck {
		return delivery{target: l.oldest(func(o *occurrence) bool {
			return o.kind == listenRequest && o.key == ack.key && !o.acknowledged
		})}
	}
	if !line.object || line.hasMethod {
		return delivery{}
	}
	key, ok := keyOf(line.id)
	if !ok || key.unmatchable {
		return delivery{}
	}
	d := delivery{target: l.oldest(func(o *occurrence) bool { return o.key == key }), ends: true}
	if line.result != nil {
		d.success = line.raw
	}
	return d
}

func (l *ledger) deliver(d delivery) {
	if d.target == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if d.ends {
		if d.target.element != nil && d.success != nil {
			l.commitLocked(d.target, d.success)
		}
		l.removeLocked(d.target)
		return
	}
	d.target.acknowledged = true
}

// drain is the crash boundary: it returns the crash error line for each
// ordinary occurrence in send order, echoing the id bytes the host sent, and
// keeps only the open listen streams, which the recovery re-issues. A
// handshake request is ordinary: the crash error is not its success, so
// nothing of it survives but what was committed before.
func (l *ledger) drain() [][]byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	var lines [][]byte
	for e := l.entries.Front(); e != nil; {
		o := e.Value.(*occurrence)
		e = e.Next()
		if o.kind == ordinaryRequest {
			lines = append(lines, crashAnswer(o.id))
		}
		if o.kind == ordinaryRequest || o.cancelled {
			l.removeLocked(o)
		}
	}
	return lines
}

// open returns the listen streams neither ended nor cancelled, oldest first.
func (l *ledger) open() []*occurrence {
	l.mu.Lock()
	defer l.mu.Unlock()
	var streams []*occurrence
	for e := l.entries.Front(); e != nil; e = e.Next() {
		if o := e.Value.(*occurrence); o.kind == listenRequest && !o.cancelled {
			streams = append(streams, o)
		}
	}
	return streams
}

func (l *ledger) isOutstanding(o *occurrence) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return o.element != nil
}

func (l *ledger) oldest(matches func(*occurrence) bool) *occurrence {
	l.mu.Lock()
	defer l.mu.Unlock()
	for e := l.entries.Front(); e != nil; e = e.Next() {
		if o := e.Value.(*occurrence); matches(o) {
			return o
		}
	}
	return nil
}

func (l *ledger) removeLocked(o *occurrence) {
	if o.element == nil {
		return
	}
	l.entries.Remove(o.element)
	o.element = nil
}
