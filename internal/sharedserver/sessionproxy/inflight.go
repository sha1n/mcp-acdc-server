package sessionproxy

import (
	"bytes"
	"encoding/json"
	"strconv"
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

// requestOf reads line as a request with an id: go-sdk reads a line as a
// request only when its method is a string.
func requestOf(line decodedLine) (requestKey, json.RawMessage, bool) {
	if !line.methodIsString {
		return requestKey{}, nil, false
	}
	key, ok := keyOf(line.id)
	return key, line.id, ok
}

// crashAnswers returns the crash error line for each request among lines, in
// their order; every other line gets none.
func crashAnswers(lines [][]byte) [][]byte {
	var answers [][]byte
	for _, line := range lines {
		if _, id, ok := requestOf(decodeLine(line)); ok {
			answers = append(answers, crashAnswer(id))
		}
	}
	return answers
}

func crashAnswer(id json.RawMessage) []byte {
	return bytes.Join([][]byte{[]byte(crashErrorPrefix), id, []byte(crashErrorSuffix)}, nil)
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
