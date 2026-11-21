package sharedserver

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/config"
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
	_, err := EncodeHelloLine("bogus")
	assert.Error(t, err)
}

func TestEncodeHelloLine_RejectsBrowseWhichNeedsItsMaxResults(t *testing.T) {
	_, err := EncodeHelloLine(HelloBrowse)
	assert.Error(t, err)
}

func TestEncodeBrowseHelloLine_CarriesUIMaxResults(t *testing.T) {
	line, err := EncodeBrowseHelloLine(25)
	require.NoError(t, err)

	assert.Equal(t, `{"hello":"browse","ui_max_results":25}`+"\n", string(line))
}

func TestEncodeBrowseHelloLine_RejectsNonPositiveMaxResults(t *testing.T) {
	for _, maxResults := range []int{0, -1} {
		_, err := EncodeBrowseHelloLine(maxResults)
		assert.Error(t, err, "max results %d", maxResults)
	}
}

func TestBrowseHelloLine_RoundTripsMaxResults(t *testing.T) {
	line, err := EncodeBrowseHelloLine(7)
	require.NoError(t, err)

	decoded, err := DecodeHelloLine(line)
	require.NoError(t, err)

	assert.Equal(t, HelloLine{Kind: HelloBrowse, UIMaxResults: 7}, decoded)
}

func TestDecodeHelloLine_RejectsBrowseWithoutPositiveMaxResults(t *testing.T) {
	for _, line := range []string{`{"hello":"browse"}`, `{"hello":"browse","ui_max_results":0}`, `{"hello":"browse","ui_max_results":-3}`} {
		_, err := DecodeHelloLine([]byte(line))
		assert.Error(t, err, line)
	}
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

func TestBrowseHelloLine_MaxResultsBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		maxResults int
		valid      bool
	}{
		{name: "lower bound", maxResults: 1, valid: true},
		{name: "upper bound", maxResults: config.MaxUIResultLimit, valid: true},
		{name: "above upper bound", maxResults: config.MaxUIResultLimit + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, encodeErr := EncodeBrowseHelloLine(tt.maxResults)
			_, decodeErr := DecodeHelloLine([]byte(fmt.Sprintf(`{"hello":"browse","ui_max_results":%d}`, tt.maxResults)))
			if tt.valid {
				assert.NoError(t, encodeErr)
				assert.NoError(t, decodeErr)
				return
			}
			assert.Error(t, encodeErr, "the client must not send an out-of-range max results")
			assert.Error(t, decodeErr, "the shared server must refuse an out-of-range max results")
		})
	}
}
