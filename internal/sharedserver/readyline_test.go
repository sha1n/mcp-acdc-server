package sharedserver

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var sessionIDPattern = regexp.MustCompile(`^[0-9a-f]{8}$`)

func TestEncodeReadyLine_IsOneJSONObjectOnOneLine(t *testing.T) {
	line, err := EncodeReadyLine("3fa9c1d2", "")
	require.NoError(t, err)

	assert.Equal(t, `{"ready":true,"session":"3fa9c1d2","log":""}`+"\n", string(line))
}

func TestReadyLine_RoundTripsSessionAndLog(t *testing.T) {
	tests := []struct {
		name    string
		session string
		log     string
	}{
		{name: "empty log", session: "0a1b2c3d", log: ""},
		{name: "absolute log path", session: "deadbeef", log: "/var/log/acdc-mcp/0123.log"},
		{name: "log path needing escapes", session: "01234567", log: "/tmp/a \"quoted\"\\path.log"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line, err := EncodeReadyLine(tt.session, tt.log)
			require.NoError(t, err)

			decoded, err := DecodeReadyLine(line)
			require.NoError(t, err)

			assert.Equal(t, ReadyLine{Ready: true, Session: tt.session, Log: tt.log}, decoded)
		})
	}
}

func TestDecodeReadyLine_RejectsInvalidLines(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{name: "not JSON", line: "hello\n"},
		{name: "not ready", line: `{"ready":false,"session":"0a1b2c3d","log":""}` + "\n"},
		{name: "missing session", line: `{"ready":true,"log":""}` + "\n"},
		{name: "JSON-RPC message", line: `{"jsonrpc":"2.0","id":1,"result":{}}` + "\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeReadyLine([]byte(tt.line))
			assert.Error(t, err)
		})
	}
}

func TestNewSessionID_IsEightLowercaseHex(t *testing.T) {
	seen := make(map[string]struct{})
	for range 200 {
		id := NewSessionID()
		assert.Regexp(t, sessionIDPattern, id)
		seen[id] = struct{}{}
	}

	assert.Greater(t, len(seen), 1, "session identifiers must be random")
}
