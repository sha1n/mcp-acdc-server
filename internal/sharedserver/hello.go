package sharedserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// HelloKind tells the shared server what a connection is for.
type HelloKind string

// HelloSession is the hello kind a session proxy sends to attach an MCP
// session.
const HelloSession HelloKind = "session"

// HelloTimeout bounds how long the shared server waits for the hello line of
// a connection before it refuses the connection.
const HelloTimeout = 5 * time.Second

// HelloLine is the first line a client writes on every shared server
// connection, before the shared server writes its ready line.
type HelloLine struct {
	Kind HelloKind `json:"hello"`
}

func EncodeHelloLine(kind HelloKind) ([]byte, error) {
	if !kind.known() {
		return nil, fmt.Errorf("failed to encode hello line: unknown kind %q", kind)
	}
	line, err := json.Marshal(HelloLine{Kind: kind})
	if err != nil {
		return nil, fmt.Errorf("failed to encode hello line: %w", err)
	}
	return append(line, '\n'), nil
}

func DecodeHelloLine(line []byte) (HelloLine, error) {
	var hello HelloLine
	if err := json.Unmarshal(line, &hello); err != nil {
		return HelloLine{}, fmt.Errorf("failed to decode hello line: %w", err)
	}
	if !hello.Kind.known() {
		return HelloLine{}, fmt.Errorf("not a hello line: unknown kind %q", hello.Kind)
	}
	return hello, nil
}

func (k HelloKind) known() bool {
	return k == HelloSession
}

// maxHelloLineLength bounds the bytes read while looking for the end of a
// hello line.
const maxHelloLineLength = 1024

// ReadHelloLine reads and decodes one hello line. It reads one byte at a
// time, so nothing after the hello line is consumed from r.
func ReadHelloLine(r io.Reader) (HelloLine, error) {
	line := make([]byte, 0, 64)
	b := make([]byte, 1)
	for len(line) < maxHelloLineLength {
		if _, err := io.ReadFull(r, b); err != nil {
			return HelloLine{}, fmt.Errorf("failed to read hello line: %w", err)
		}
		if b[0] == '\n' {
			return DecodeHelloLine(line)
		}
		line = append(line, b[0])
	}
	return HelloLine{}, errors.New("failed to read hello line: line too long")
}
