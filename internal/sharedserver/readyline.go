package sharedserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// ReadyLine is the first line a shared server writes on every connection,
// before any JSON-RPC traffic.
type ReadyLine struct {
	Ready   bool   `json:"ready"`
	Session string `json:"session"`
	Log     string `json:"log"`
}

func EncodeReadyLine(session, logPath string) ([]byte, error) {
	line, err := json.Marshal(ReadyLine{Ready: true, Session: session, Log: logPath})
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

// NewSessionID mints the identifier of one attached session: 8 random
// lowercase hex characters.
func NewSessionID() string {
	b := make([]byte, 4)
	// crypto/rand.Read is documented to never return an error.
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
