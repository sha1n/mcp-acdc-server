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

func TestEncodeBrowseReadyLine_CarriesUIURLWithoutSession(t *testing.T) {
	line, err := EncodeBrowseReadyLine("http://127.0.0.1:4321/ui/", "/tmp/x.log")
	require.NoError(t, err)

	assert.Equal(t, `{"ready":true,"log":"/tmp/x.log","ui_url":"http://127.0.0.1:4321/ui/"}`+"\n", string(line))
}

func TestEncodeBrowseErrorLine_CarriesErrorAndIsNotReady(t *testing.T) {
	line, err := EncodeBrowseErrorLine("failed to start the web UI: boom", "/tmp/x.log")
	require.NoError(t, err)

	assert.Equal(t, `{"ready":false,"log":"/tmp/x.log","error":"failed to start the web UI: boom"}`+"\n", string(line))
}

func TestDecodeBrowseReadyLine_ReturnsUIURL(t *testing.T) {
	line, err := EncodeBrowseReadyLine("http://127.0.0.1:4321/ui/", "/tmp/x.log")
	require.NoError(t, err)

	ready, err := DecodeBrowseReadyLine(line)
	require.NoError(t, err)

	assert.Equal(t, ReadyLine{Ready: true, Log: "/tmp/x.log", UIURL: "http://127.0.0.1:4321/ui/"}, ready)
}

func TestDecodeBrowseReadyLine_ReturnsTheReportedErrorWithTheLog(t *testing.T) {
	line, err := EncodeBrowseErrorLine("failed to start the web UI: boom", "/tmp/x.log")
	require.NoError(t, err)

	ready, err := DecodeBrowseReadyLine(line)

	require.Error(t, err)
	assert.Equal(t, "failed to start the web UI: boom", err.Error())
	assert.Equal(t, "/tmp/x.log", ready.Log)
}

func TestDecodeBrowseReadyLine_RejectsInvalidLines(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{name: "not JSON", line: "hello\n"},
		{name: "session ready line", line: `{"ready":true,"session":"0a1b2c3d","log":""}` + "\n"},
		{name: "not ready without error", line: `{"ready":false,"log":""}` + "\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeBrowseReadyLine([]byte(tt.line))
			assert.Error(t, err)
		})
	}
}

func TestDecodeReadyLine_RejectsBrowseReadyLine(t *testing.T) {
	line, err := EncodeBrowseReadyLine("http://127.0.0.1:4321/ui/", "")
	require.NoError(t, err)

	_, err = DecodeReadyLine(line)
	assert.Error(t, err)
}
