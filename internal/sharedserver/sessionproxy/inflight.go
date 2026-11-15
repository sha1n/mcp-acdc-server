package sessionproxy

import (
	"bytes"
	"container/list"
	"encoding/json"
	"strconv"
	"sync"
)

const crashErrorPrefix = `{"jsonrpc":"2.0","id":`

const crashErrorSuffix = `,"error":{"code":-32000,"message":"shared server connection lost before a response was received"}}` + "\n"

// requestKey is a request id as the shared server's go-sdk reads it: a
// number goes through float64 and then int64, so a response may carry
// different bytes than the request did (1.5 and 1e2 come back as 1 and 100).
type requestKey struct {
	text   string
	number int64
	isText bool
	// unmatchable marks a number go-sdk cannot decode: no response with
	// that id ever comes.
	unmatchable bool
}

type inFlightEntry struct {
	key requestKey
	id  json.RawMessage
}

// inFlight holds the requests written to the shared server whose response
// has not yet been written to the host, oldest first.
type inFlight struct {
	mu      sync.Mutex
	entries list.List
}

func newInFlight() *inFlight {
	return &inFlight{}
}

// track records line if it is a request with an id. The returned handle
// undoes it when the line could not be fully written; it is nil for any
// other line, which is forwarded untracked.
func (f *inFlight) track(line []byte) *list.Element {
	key, id, ok := requestOf(line)
	if !ok {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.entries.PushBack(inFlightEntry{key: key, id: id})
}

// requestOf reads line as a request with an id: go-sdk reads a line as a
// request only when its method is a string.
func requestOf(line []byte) (requestKey, json.RawMessage, bool) {
	members, ok := objectMembers(line)
	if !ok || !isString(members["method"]) {
		return requestKey{}, nil, false
	}
	key, ok := keyOf(members["id"])
	return key, members["id"], ok
}

func (f *inFlight) untrack(handle *list.Element) {
	if handle == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries.Remove(handle)
}

// answered clears the oldest request the server line responds to, if any.
func (f *inFlight) answered(line []byte) {
	members, ok := objectMembers(line)
	if !ok {
		return
	}
	if _, isRequest := members["method"]; isRequest {
		return
	}
	key, ok := keyOf(members["id"])
	if !ok || key.unmatchable {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for e := f.entries.Front(); e != nil; e = e.Next() {
		if e.Value.(inFlightEntry).key == key {
			f.entries.Remove(e)
			return
		}
	}
}

// drain empties the set and returns the crash error line for each request
// in it, in send order, echoing the id bytes the host sent.
func (f *inFlight) drain() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	var lines [][]byte
	for e := f.entries.Front(); e != nil; e = e.Next() {
		lines = append(lines, crashAnswer(e.Value.(inFlightEntry).id))
	}
	f.entries.Init()
	return lines
}

// crashAnswers returns the crash error line for each request among lines, in
// their order; every other line gets none.
func crashAnswers(lines [][]byte) [][]byte {
	var answers [][]byte
	for _, line := range lines {
		if _, id, ok := requestOf(line); ok {
			answers = append(answers, crashAnswer(id))
		}
	}
	return answers
}

func crashAnswer(id json.RawMessage) []byte {
	return bytes.Join([][]byte{[]byte(crashErrorPrefix), id, []byte(crashErrorSuffix)}, nil)
}

// objectMembers decodes into a map because go-sdk matches member names
// case-sensitively, unlike decoding into a struct.
func objectMembers(line []byte) (map[string]json.RawMessage, bool) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(line, &members); err != nil || members == nil {
		return nil, false
	}
	return members, true
}

// isString needs only the first byte: objectMembers has already validated
// the whole value.
func isString(value json.RawMessage) bool {
	return len(value) > 0 && value[0] == '"'
}

func keyOf(id json.RawMessage) (requestKey, bool) {
	if len(id) == 0 {
		return requestKey{}, false
	}
	switch id[0] {
	case '"':
		var text string
		err := json.Unmarshal(id, &text)
		return requestKey{text: text, isText: true}, err == nil
	case 'n', 't', 'f', '{', '[':
		return requestKey{}, false
	}
	number, err := strconv.ParseFloat(string(id), 64)
	if err != nil {
		return requestKey{unmatchable: true}, true
	}
	return requestKey{number: int64(number)}, true
}
