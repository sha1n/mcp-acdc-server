package sessionproxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
)

const (
	methodInitialize  = "initialize"
	methodInitialized = "notifications/initialized"
	methodSetLevel    = "logging/setLevel"
)

// handshakeReplay is what a legacy host negotiated, as the host saw it.
type handshakeReplay struct {
	initialize  []byte
	result      []byte
	initialized []byte
	setLevel    []byte
}

// initializedRecord is what recording a notifications/initialized replaced,
// so that a failed write restores exactly that.
type initializedRecord struct {
	recorded bool
	previous []byte
}

// recordInitialized puts a notifications/initialized in effect before its
// write, as a request is registered: only the host writes to the server, so
// undoInitialized after a failed write restores exactly the one in effect.
func (l *ledger) recordInitialized(line decodedLine) initializedRecord {
	if !line.methodIs(methodInitialized) {
		return initializedRecord{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	record := initializedRecord{recorded: true, previous: l.committed.initialized}
	l.committed.initialized = slices.Clone(line.raw)
	return record
}

func (l *ledger) undoInitialized(record initializedRecord) {
	if !record.recorded {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.committed.initialized = record.previous
}

// commitLocked puts in effect the handshake request whose success reached
// the host.
func (l *ledger) commitLocked(o *occurrence, success []byte) {
	switch o.handshake {
	case methodSetLevel:
		l.committed.setLevel = o.line
	case methodInitialize:
		l.committed.initialize = o.line
		l.committed.result = slices.Clone(success)
	}
}

// replay reports a legacy session: one whose host saw an initialize
// succeed.
func (l *ledger) replay() (handshakeReplay, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.committed
	return handshakeReplay{
		initialize:  slices.Clone(c.initialize),
		result:      slices.Clone(c.result),
		initialized: slices.Clone(c.initialized),
		setLevel:    slices.Clone(c.setLevel),
	}, c.initialize != nil
}

var errInitializeRejected = errors.New("the replacement shared server rejected the replayed initialize")

// comparedMembers are the members of an initialize result a host acts on;
// a difference in them is reported, while the host keeps what it first saw.
var comparedMembers = []string{"capabilities", "instructions", "serverInfo"}

// compareInitialize names the compared members in which the replayed result
// differs from the one the host saw. A different protocol version, or no
// result at all, leaves the session unable to continue on the new server.
func compareInitialize(seen, replayed decodedLine) ([]string, error) {
	seenResult, _ := objectMembers(seen.result)
	replayedResult, ok := objectMembers(replayed.result)
	if !ok {
		return nil, &discoveryError{cause: causeProtocolVersion, err: errInitializeRejected}
	}
	var seenVersion, replayedVersion string
	_ = json.Unmarshal(seenResult["protocolVersion"], &seenVersion)
	if json.Unmarshal(replayedResult["protocolVersion"], &replayedVersion) != nil {
		return nil, &discoveryError{cause: causeProtocolVersion, err: fmt.Errorf(
			"the replayed initialize result carries no protocol version, the host saw %q", seenVersion)}
	}
	if replayedVersion != seenVersion {
		return nil, &discoveryError{cause: causeProtocolVersion, err: fmt.Errorf(
			"the replayed initialize negotiated protocol version %q, the host saw %q", replayedVersion, seenVersion)}
	}
	var differs []string
	for _, member := range comparedMembers {
		if !sameValue(seenResult[member], replayedResult[member]) {
			differs = append(differs, member)
		}
	}
	return differs, nil
}

// objectMembers reads a nested member of a decoded line.
func objectMembers(value json.RawMessage) (map[string]json.RawMessage, bool) {
	var members map[string]json.RawMessage
	if json.Unmarshal(value, &members) != nil || members == nil {
		return nil, false
	}
	return members, true
}

func sameValue(a, b json.RawMessage) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	var left, right any
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && reflect.DeepEqual(left, right)
}

// listChangeAnnouncements excludes tools: the binary fixes them.
var listChangeAnnouncements = []struct {
	capability string
	line       string
}{
	{capability: "resources", line: `{"jsonrpc":"2.0","method":"notifications/resources/list_changed"}` + "\n"},
	{capability: "prompts", line: `{"jsonrpc":"2.0","method":"notifications/prompts/list_changed"}` + "\n"},
}

// announcements are the list changes the result the host saw lets it
// receive.
func announcements(result []byte) [][]byte {
	members, _ := objectMembers(decodeLine(result).result)
	capabilities, _ := objectMembers(members["capabilities"])
	var lines [][]byte
	for _, announcement := range listChangeAnnouncements {
		capability, _ := objectMembers(capabilities[announcement.capability])
		var listChanged bool
		if json.Unmarshal(capability["listChanged"], &listChanged) == nil && listChanged {
			lines = append(lines, []byte(announcement.line))
		}
	}
	return lines
}
