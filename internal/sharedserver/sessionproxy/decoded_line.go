package sessionproxy

import "encoding/json"

// decodedLine is a relayed line parsed once: every observer of the line reads
// these top-level members instead of parsing the line again, since a line may
// be megabytes long. Nested members stay raw for the observer that needs them.
type decodedLine struct {
	raw []byte
	// object is false for a line that is not a single JSON object; every
	// other member is then empty.
	object bool
	// hasMethod records the member's presence: go-sdk reads a line with any
	// method member as a message, not a response.
	hasMethod bool
	// method is set only when the method member is a string.
	method         string
	methodIsString bool
	id             json.RawMessage
	params         json.RawMessage
	result         json.RawMessage
}

// decodeLine decodes into a map because go-sdk matches member names
// case-sensitively, unlike decoding into a struct.
func decodeLine(line []byte) decodedLine {
	decoded := decodedLine{raw: line}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(line, &members); err != nil || members == nil {
		return decoded
	}
	decoded.object = true
	method, hasMethod := members["method"]
	decoded.hasMethod = hasMethod
	decoded.methodIsString = isString(method) && json.Unmarshal(method, &decoded.method) == nil
	decoded.id = members["id"]
	decoded.params = members["params"]
	decoded.result = members["result"]
	return decoded
}

func (d decodedLine) methodIs(name string) bool {
	return d.methodIsString && d.method == name
}

// isString needs only the first byte: the line decode has already validated
// the whole value.
func isString(value json.RawMessage) bool {
	return len(value) > 0 && value[0] == '"'
}
