package sharedserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
)

// HelloKind tells the shared server what a connection is for.
type HelloKind string

// HelloSession is the hello kind a session proxy sends to attach an MCP
// session.
const HelloSession HelloKind = "session"

// HelloBrowse is the hello kind a client sends to get the URL of the shared
// server's Web UI. A browse connection is not an attached session: the
// shared server answers with one ready line and closes it.
const HelloBrowse HelloKind = "browse"

// HelloTimeout bounds how long the shared server waits for the hello line of
// a connection before it refuses the connection.
const HelloTimeout = 5 * time.Second

// HelloLine is the first line a client writes on every shared server
// connection, before the shared server writes its ready line.
type HelloLine struct {
	Kind HelloKind `json:"hello"`
	// UIMaxResults is the requester's ui.max_results. It is carried, and
	// required to be within the ui.max_results range, only by a browse hello.
	UIMaxResults int `json:"ui_max_results,omitempty"`
}

// EncodeHelloLine encodes a hello line that carries nothing but its kind; a
// browse hello is encoded with EncodeBrowseHelloLine.
func EncodeHelloLine(kind HelloKind) ([]byte, error) {
	if kind != HelloSession {
		return nil, fmt.Errorf("failed to encode hello line: unknown kind %q", kind)
	}
	return encodeHello(HelloLine{Kind: kind})
}

// EncodeBrowseHelloLine encodes the hello line of a browse request carrying
// the requester's ui.max_results.
func EncodeBrowseHelloLine(uiMaxResults int) ([]byte, error) {
	hello := HelloLine{Kind: HelloBrowse, UIMaxResults: uiMaxResults}
	if err := hello.validate(); err != nil {
		return nil, fmt.Errorf("failed to encode hello line: %w", err)
	}
	return encodeHello(hello)
}

func encodeHello(hello HelloLine) ([]byte, error) {
	line, err := json.Marshal(hello)
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
	if err := hello.validate(); err != nil {
		return HelloLine{}, fmt.Errorf("not a hello line: %w", err)
	}
	return hello, nil
}

func (h HelloLine) validate() error {
	switch h.Kind {
	case HelloSession:
		return nil
	case HelloBrowse:
		if h.UIMaxResults < 1 || h.UIMaxResults > config.MaxUIResultLimit {
			return fmt.Errorf("browse requires a ui_max_results between 1 and %d, got: %d", config.MaxUIResultLimit, h.UIMaxResults)
		}
		return nil
	default:
		return fmt.Errorf("unknown kind %q", h.Kind)
	}
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
