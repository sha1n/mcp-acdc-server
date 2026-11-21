package sharedserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// ReadyLine is the first line a shared server writes on every connection,
// before any JSON-RPC traffic. A session gets its Session; a browse request
// gets UIURL, or Ready false and the Error that prevented the Web UI.
type ReadyLine struct {
	Ready   bool   `json:"ready"`
	Session string `json:"session,omitempty"`
	Log     string `json:"log"`
	UIURL   string `json:"ui_url,omitempty"`
	Error   string `json:"error,omitempty"`
}

func EncodeReadyLine(session, logPath string) ([]byte, error) {
	return encodeReady(ReadyLine{Ready: true, Session: session, Log: logPath})
}

// EncodeBrowseReadyLine encodes the reply to a browse request whose Web UI
// is running at uiURL.
func EncodeBrowseReadyLine(uiURL, logPath string) ([]byte, error) {
	return encodeReady(ReadyLine{Ready: true, Log: logPath, UIURL: uiURL})
}

// EncodeBrowseErrorLine encodes the reply to a browse request whose Web UI
// could not be started.
func EncodeBrowseErrorLine(message, logPath string) ([]byte, error) {
	return encodeReady(ReadyLine{Log: logPath, Error: message})
}

func encodeReady(ready ReadyLine) ([]byte, error) {
	line, err := json.Marshal(ready)
	if err != nil {
		return nil, fmt.Errorf("failed to encode ready line: %w", err)
	}
	return append(line, '\n'), nil
}

func DecodeReadyLine(line []byte) (ReadyLine, error) {
	var ready ReadyLine
	if err := json.Unmarshal(line, &ready); err != nil {
		return ReadyLine{}, fmt.Errorf("failed to decode ready line: %w", err)
	}
	if !ready.Ready || ready.Session == "" {
		return ReadyLine{}, errors.New("not a ready line")
	}
	return ready, nil
}

// DecodeBrowseReadyLine decodes the reply to a browse request. A reply that
// reports a failure returns that failure as the error, together with the
// decoded line so the caller can point at its log.
func DecodeBrowseReadyLine(line []byte) (ReadyLine, error) {
	var ready ReadyLine
	if err := json.Unmarshal(line, &ready); err != nil {
		return ReadyLine{}, fmt.Errorf("failed to decode ready line: %w", err)
	}
	if ready.Error != "" {
		return ready, errors.New(ready.Error)
	}
	if !ready.Ready || ready.UIURL == "" {
		return ReadyLine{}, errors.New("not a browse ready line")
	}
	return ready, nil
}

// NewSessionID mints the identifier of one attached session: 8 random
// lowercase hex characters.
func NewSessionID() string {
	b := make([]byte, 4)
	// crypto/rand.Read is documented to never return an error.
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
