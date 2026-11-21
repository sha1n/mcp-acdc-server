package sharedserver

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeHelloLine_IsOneJSONObjectOnOneLine(t *testing.T) {
	line, err := EncodeHelloLine(HelloSession)
	require.NoError(t, err)

	assert.Equal(t, `{"hello":"session"}`+"\n", string(line))
}

func TestHelloLine_RoundTripsKind(t *testing.T) {
	line, err := EncodeHelloLine(HelloSession)
	require.NoError(t, err)

	decoded, err := DecodeHelloLine(line)
	require.NoError(t, err)

	assert.Equal(t, HelloLine{Kind: HelloSession}, decoded)
}

func TestDecodeHelloLine_AcceptsLineWithoutTrailingNewline(t *testing.T) {
	decoded, err := DecodeHelloLine([]byte(`{"hello":"session"}`))
	require.NoError(t, err)

	assert.Equal(t, HelloSession, decoded.Kind)
}

func TestEncodeHelloLine_RejectsUnknownKind(t *testing.T) {
	_, err := EncodeHelloLine("browse")
	assert.Error(t, err)
}

func TestDecodeHelloLine_RejectsInvalidLines(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{name: "empty", line: "\n"},
		{name: "not JSON", line: "hello\n"},
		{name: "missing kind", line: `{}` + "\n"},
		{name: "empty kind", line: `{"hello":""}` + "\n"},
		{name: "unknown kind", line: `{"hello":"browse"}` + "\n"},
		{name: "kind of the wrong type", line: `{"hello":1}` + "\n"},
		{name: "ready line", line: `{"ready":true,"session":"0a1b2c3d","log":""}` + "\n"},
		{name: "JSON-RPC message", line: `{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeHelloLine([]byte(tt.line))
			assert.Error(t, err)
		})
	}
}

func TestReadHelloLine_LeavesWhatFollowsTheHelloUnread(t *testing.T) {
	r := strings.NewReader(`{"hello":"session"}` + "\n" + `{"jsonrpc":"2.0"}` + "\n")

	hello, err := ReadHelloLine(r)
	require.NoError(t, err)
	assert.Equal(t, HelloSession, hello.Kind)

	rest, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, `{"jsonrpc":"2.0"}`+"\n", string(rest))
}

func TestReadHelloLine_RejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: "EOF"},
		{name: "no line end", input: `{"hello":"session"}`, want: "EOF"},
		{name: "malformed", input: "nonsense\n", want: "failed to decode hello line"},
		{name: "too long", input: strings.Repeat("x", 2*maxHelloLineLength) + "\n", want: "line too long"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ReadHelloLine(strings.NewReader(tt.input))
			assert.ErrorContains(t, err, tt.want)
		})
	}
}
