package sessionproxy

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func crashErrorLine(id string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"error":{"code":-32000,"message":"shared server connection lost before a response was received"}}` + "\n"
}

// sdkEcho is the id the shared server's go-sdk writes in its response to a
// request carrying id: the in-flight set must match exactly that.
func sdkEcho(t *testing.T, id string) string {
	t.Helper()
	msg, err := jsonrpc.DecodeMessage([]byte(`{"jsonrpc":"2.0","id":` + id + `,"method":"ping"}`))
	require.NoError(t, err)
	request, ok := msg.(*jsonrpc.Request)
	require.True(t, ok)
	require.True(t, request.IsCall())
	encoded, err := jsonrpc.EncodeMessage(&jsonrpc.Response{ID: request.ID, Result: json.RawMessage(`{}`)})
	require.NoError(t, err)
	var response struct {
		ID json.RawMessage `json:"id"`
	}
	require.NoError(t, json.Unmarshal(encoded, &response))
	return string(response.ID)
}

func TestInFlight_ClearsARequestWithTheIdTheSharedServerEchoes(t *testing.T) {
	cases := []struct {
		name string
		id   string
		// echo is empty where the value depends on the platform's float to
		// int64 conversion; the go-sdk echo is then the only reference.
		echo string
	}{
		{name: "integer", id: `1`, echo: `1`},
		{name: "negative integer", id: `-7`, echo: `-7`},
		{name: "zero with sign", id: `-0`, echo: `0`},
		{name: "integral fraction", id: `1.0`, echo: `1`},
		{name: "fraction", id: `1.5`, echo: `1`},
		{name: "negative fraction", id: `-1.5`, echo: `-1`},
		{name: "exponent", id: `1e2`, echo: `100`},
		{name: "upper case exponent", id: `1E2`, echo: `100`},
		{name: "beyond 2^53", id: `9007199254740993`, echo: `9007199254740992`},
		{name: "beyond int64", id: `9223372036854775808`},
		{name: "huge exponent", id: `1e300`},
		{name: "string", id: `"q-1"`, echo: `"q-1"`},
		{name: "escaped string", id: `"A"`, echo: `"A"`},
		{name: "empty string", id: `""`, echo: `""`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			echo := sdkEcho(t, tc.id)
			if tc.echo != "" {
				require.Equal(t, tc.echo, echo, "go-sdk echo")
			}
			requests := newInFlight()
			require.NotNil(t, requests.track(decodeLine([]byte(`{"jsonrpc":"2.0","id":`+tc.id+`,"method":"tools/call"}`+"\n"))))

			requests.answered(decodeLine([]byte(`{"jsonrpc":"2.0","id":` + echo + `,"result":{}}` + "\n")))

			assert.Empty(t, requests.drain())
		})
	}
}

func TestInFlight_AnswersAnUnansweredRequestWithItsOriginalIdBytes(t *testing.T) {
	cases := []struct {
		name string
		line string
		id   string
	}{
		{name: "integer", line: `{"jsonrpc":"2.0","id":1,"method":"tools/call"}`, id: `1`},
		{name: "fraction", line: `{"jsonrpc":"2.0","id":1.5,"method":"tools/call"}`, id: `1.5`},
		{name: "exponent", line: `{"jsonrpc":"2.0","id":1e2,"method":"tools/call"}`, id: `1e2`},
		{name: "beyond 2^53", line: `{"jsonrpc":"2.0","id":9007199254740993,"method":"x"}`, id: `9007199254740993`},
		{name: "unrepresentable number", line: `{"jsonrpc":"2.0","id":1e400,"method":"x"}`, id: `1e400`},
		{name: "string with escape", line: `{"jsonrpc":"2.0","id":"A","method":"x"}`, id: `"A"`},
		{name: "whitespace around members", line: `{ "id" : 3 , "method" : "x" }`, id: `3`},
		{name: "no jsonrpc member", line: `{"id":4,"method":"x"}`, id: `4`},
		{name: "CRLF terminated", line: "{\"jsonrpc\":\"2.0\",\"id\":6,\"method\":\"x\"}\r", id: `6`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := newInFlight()
			require.NotNil(t, requests.track(decodeLine([]byte(tc.line+"\n"))))

			lines := requests.drain()

			require.Len(t, lines, 1)
			assert.Equal(t, crashErrorLine(tc.id), string(lines[0]))
			assert.Empty(t, requests.drain(), "drain empties the set")
		})
	}
}

func TestInFlight_DoesNotTrackALineThatIsNotARequestWithAnId(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{name: "notification", line: `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`},
		{name: "null id", line: `{"jsonrpc":"2.0","id":null,"method":"tools/call"}`},
		{name: "boolean id", line: `{"jsonrpc":"2.0","id":true,"method":"tools/call"}`},
		{name: "object id", line: `{"jsonrpc":"2.0","id":{},"method":"tools/call"}`},
		{name: "array id", line: `{"jsonrpc":"2.0","id":[1],"method":"tools/call"}`},
		{name: "id key in another case", line: `{"jsonrpc":"2.0","ID":1,"method":"tools/call"}`},
		{name: "number method", line: `{"jsonrpc":"2.0","id":5,"method":7}`},
		{name: "null method", line: `{"jsonrpc":"2.0","id":5,"method":null}`},
		{name: "boolean method", line: `{"jsonrpc":"2.0","id":5,"method":true}`},
		{name: "object method", line: `{"jsonrpc":"2.0","id":5,"method":{}}`},
		{name: "array method", line: `{"jsonrpc":"2.0","id":5,"method":["tools/call"]}`},
		{name: "host response", line: `{"jsonrpc":"2.0","id":1,"result":{}}`},
		{name: "host error response", line: `{"jsonrpc":"2.0","id":1,"error":{"code":-1,"message":"no"}}`},
		{name: "batch", line: `[{"jsonrpc":"2.0","id":1,"method":"tools/call"}]`},
		{name: "string", line: `"tools/call"`},
		{name: "number", line: `42`},
		{name: "null", line: `null`},
		{name: "malformed", line: `{"jsonrpc":"2.0","id":1,"method":`},
		{name: "empty line", line: ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := newInFlight()

			assert.Nil(t, requests.track(decodeLine([]byte(tc.line+"\n"))))
			assert.Empty(t, requests.drain())
		})
	}
}

func TestInFlight_ClearsOnlyOnAResponseFromTheServer(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		cleared bool
	}{
		{name: "result", line: `{"jsonrpc":"2.0","id":1,"result":{}}`, cleared: true},
		{name: "error", line: `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"bad"}}`, cleared: true},
		{name: "other id", line: `{"jsonrpc":"2.0","id":2,"result":{}}`},
		{name: "same digits as a string", line: `{"jsonrpc":"2.0","id":"1","result":{}}`},
		{name: "null id error", line: `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse"}}`},
		{name: "server request with the same id", line: `{"jsonrpc":"2.0","id":1,"method":"roots/list"}`},
		{name: "notification", line: `{"jsonrpc":"2.0","method":"notifications/resources/list_changed"}`},
		{name: "batch", line: `[{"jsonrpc":"2.0","id":1,"result":{}}]`},
		{name: "malformed", line: `{"jsonrpc":"2.0","id":1,"res`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := newInFlight()
			require.NotNil(t, requests.track(decodeLine([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`+"\n"))))

			requests.answered(decodeLine([]byte(tc.line + "\n")))

			if tc.cleared {
				assert.Empty(t, requests.drain())
				return
			}
			assert.Equal(t, []string{crashErrorLine(`1`)}, lineStrings(requests.drain()))
		})
	}
}

func TestInFlight_StringIdDoesNotMatchANumericResponse(t *testing.T) {
	requests := newInFlight()
	require.NotNil(t, requests.track(decodeLine([]byte(`{"id":"1","method":"x"}`+"\n"))))

	requests.answered(decodeLine([]byte(`{"id":1,"result":{}}` + "\n")))

	assert.Equal(t, []string{crashErrorLine(`"1"`)}, lineStrings(requests.drain()))
}

func TestInFlight_DuplicateIdsAreAnsweredOncePerRequestOldestFirst(t *testing.T) {
	requests := newInFlight()
	require.NotNil(t, requests.track(decodeLine([]byte(`{"id":1.0,"method":"x"}`+"\n"))))
	require.NotNil(t, requests.track(decodeLine([]byte(`{"id":1,"method":"x"}`+"\n"))))
	require.NotNil(t, requests.track(decodeLine([]byte(`{"id":1,"method":"x"}`+"\n"))))

	requests.answered(decodeLine([]byte(`{"id":1,"result":{}}` + "\n")))

	assert.Equal(t, []string{crashErrorLine(`1`), crashErrorLine(`1`)}, lineStrings(requests.drain()))
}

func TestInFlight_AnswersRemainingRequestsInSendOrder(t *testing.T) {
	requests := newInFlight()
	for _, id := range []string{`"a"`, `2`, `"c"`, `4`} {
		require.NotNil(t, requests.track(decodeLine([]byte(`{"id":`+id+`,"method":"x"}`+"\n"))))
	}

	requests.answered(decodeLine([]byte(`{"id":2,"result":{}}` + "\n")))
	requests.answered(decodeLine([]byte(`{"id":"c","error":{"code":1,"message":"m"}}` + "\n")))

	assert.Equal(t, []string{crashErrorLine(`"a"`), crashErrorLine(`4`)}, lineStrings(requests.drain()))
}

func TestInFlight_UntrackRemovesOnlyThatRequest(t *testing.T) {
	requests := newInFlight()
	first := requests.track(decodeLine([]byte(`{"id":1,"method":"x"}` + "\n")))
	second := requests.track(decodeLine([]byte(`{"id":1,"method":"x"}` + "\n")))
	require.NotNil(t, first)
	require.NotNil(t, second)

	requests.untrack(second)
	requests.untrack(nil)

	assert.Equal(t, []string{crashErrorLine(`1`)}, lineStrings(requests.drain()))
}

func TestInFlight_ClearedEntriesDoNotAccumulate(t *testing.T) {
	requests := newInFlight()
	for range 1000 {
		require.NotNil(t, requests.track(decodeLine([]byte(`{"id":7,"method":"x"}`+"\n"))))
		requests.answered(decodeLine([]byte(`{"id":7,"result":{}}` + "\n")))
		requests.untrack(requests.track(decodeLine([]byte(`{"id":8,"method":"x"}` + "\n"))))
	}

	assert.Zero(t, requests.entries.Len(), "no entry is retained")
}

func lineStrings(lines [][]byte) []string {
	var out []string
	for _, line := range lines {
		out = append(out, string(line))
	}
	return out
}
