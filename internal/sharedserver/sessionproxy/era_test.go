package sessionproxy

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	eraInitialize  = `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}` + "\n"
	eraInitialized = `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	eraResult      = `{"jsonrpc":"2.0","id":0,"result":{"protocolVersion":"2025-11-25","capabilities":{"resources":{"listChanged":true}},"serverInfo":{"name":"acdc","version":"1"}}}` + "\n"
)

func eraSetLevel(id, level string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"method":"logging/setLevel","params":{"level":"` + level + `"}}` + "\n"
}

func eraSuccess(id string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"result":{}}` + "\n"
}

func eraFailure(id string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"error":{"code":-32602,"message":"rejected"}}` + "\n"
}

type observed struct {
	toServer bool
	line     string
}

func server(line string) observed { return observed{toServer: true, line: line} }

func host(line string) observed { return observed{line: line} }

func observe(lines ...observed) *handshake {
	h := &handshake{}
	for _, o := range lines {
		if o.toServer {
			h.sentToServer([]byte(o.line))
		} else {
			h.sentToHost([]byte(o.line))
		}
	}
	return h
}

func TestHandshake_ReplaysOnlyWhatTheHostSawSucceed(t *testing.T) {
	cases := []struct {
		name     string
		observed []observed
		legacy   bool
		want     handshakeReplay
	}{
		{name: "no initialize observed is the modern era", observed: []observed{server(eraSetLevel("1", "debug")), host(eraSuccess("1"))}},
		{name: "initialize whose result has not reached the host", observed: []observed{server(eraInitialize)}},
		{name: "initialize answered with an error", observed: []observed{server(eraInitialize), host(eraFailure("0"))}},
		{
			name:     "initialize answered with a result",
			observed: []observed{server(eraInitialize), host(eraResult)},
			legacy:   true,
			want:     handshakeReplay{initialize: []byte(eraInitialize), result: []byte(eraResult)},
		},
		{
			name:     "initialized and a successful setLevel",
			observed: []observed{server(eraInitialize), host(eraResult), server(eraInitialized), server(eraSetLevel("1", "debug")), host(eraSuccess("1"))},
			legacy:   true,
			want:     handshakeReplay{initialize: []byte(eraInitialize), result: []byte(eraResult), initialized: []byte(eraInitialized), setLevel: []byte(eraSetLevel("1", "debug"))},
		},
		{
			name: "the last setLevel answered with success, not a later rejected or unanswered one",
			observed: []observed{
				server(eraInitialize), host(eraResult),
				server(eraSetLevel("1", "info")), host(eraSuccess("1")),
				server(eraSetLevel("2", "debug")), host(eraSuccess("2")),
				server(eraSetLevel("3", "error")), host(eraFailure("3")),
				server(eraSetLevel("4", "warning")),
			},
			legacy: true,
			want:   handshakeReplay{initialize: []byte(eraInitialize), result: []byte(eraResult), setLevel: []byte(eraSetLevel("2", "debug"))},
		},
		{
			name: "a later successful initialize replaces the commit and a rejected one does not",
			observed: []observed{
				server(eraInitialize), host(eraResult),
				server(`{"jsonrpc":"2.0","id":"again","method":"initialize","params":{}}` + "\n"), host(eraSuccess(`"again"`)),
				server(`{"jsonrpc":"2.0","id":9,"method":"initialize","params":{}}` + "\n"), host(eraFailure("9")),
			},
			legacy: true,
			want: handshakeReplay{
				initialize: []byte(`{"jsonrpc":"2.0","id":"again","method":"initialize","params":{}}` + "\n"),
				result:     []byte(eraSuccess(`"again"`)),
			},
		},
		{
			name:     "a response with another id commits nothing",
			observed: []observed{server(eraInitialize), host(eraSuccess("1"))},
		},
		{
			name:     "a number id matches as the shared server reads it",
			observed: []observed{server(`{"jsonrpc":"2.0","id":1.0,"method":"initialize"}` + "\n"), host(eraSuccess("1"))},
			legacy:   true,
			want:     handshakeReplay{initialize: []byte(`{"jsonrpc":"2.0","id":1.0,"method":"initialize"}` + "\n"), result: []byte(eraSuccess("1"))},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			replay, legacy := observe(tc.observed...).replay()
			assert.Equal(t, tc.legacy, legacy)
			if tc.legacy {
				assert.Equal(t, tc.want, replay)
			}
		})
	}
}

func TestHandshake_ReplayIsACopy(t *testing.T) {
	h := observe(server(eraInitialize), host(eraResult))
	replay, _ := h.replay()
	replay.initialize[0] = 'x'
	again, _ := h.replay()
	assert.Equal(t, eraInitialize, string(again.initialize))
}

func resultLine(result string) []byte {
	return []byte(`{"jsonrpc":"2.0","id":0,"result":` + result + `}` + "\n")
}

func TestCompareInitialize(t *testing.T) {
	seen := `{"protocolVersion":"2025-11-25","capabilities":{"prompts":{"listChanged":true},"resources":{"listChanged":true}},"instructions":"use search","serverInfo":{"name":"acdc","version":"1"}}`
	cases := []struct {
		name     string
		replayed string
		differs  []string
	}{
		{name: "identical", replayed: seen},
		{
			name:     "same values with other key order, spacing and unrelated members",
			replayed: `{ "serverInfo":{"version":"1","name":"acdc"}, "instructions":"use search", "_meta":{"x":1}, "capabilities":{"resources":{"listChanged":true},"prompts":{"listChanged":true}}, "protocolVersion":"2025-11-25" }`,
		},
		{
			name:     "different instructions",
			replayed: `{"protocolVersion":"2025-11-25","capabilities":{"prompts":{"listChanged":true},"resources":{"listChanged":true}},"instructions":"other","serverInfo":{"name":"acdc","version":"1"}}`,
			differs:  []string{"instructions"},
		},
		{
			name:     "capabilities and serverInfo differ, instructions absent",
			replayed: `{"protocolVersion":"2025-11-25","capabilities":{"prompts":{}},"serverInfo":{"name":"acdc","version":"2"}}`,
			differs:  []string{"capabilities", "instructions", "serverInfo"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			differs, err := compareInitialize(resultLine(seen), resultLine(tc.replayed))
			require.NoError(t, err)
			assert.Equal(t, tc.differs, differs)
		})
	}
}

func TestCompareInitialize_FailsTheRecoveryOnAnotherProtocolVersionOrARejection(t *testing.T) {
	seen := resultLine(`{"protocolVersion":"2025-11-25","capabilities":{}}`)
	cases := []struct {
		name     string
		replayed []byte
		message  string
	}{
		{name: "different version", replayed: resultLine(`{"protocolVersion":"2025-06-18","capabilities":{}}`), message: "2025-06-18"},
		{name: "absent version", replayed: resultLine(`{"capabilities":{}}`), message: "protocol version"},
		{name: "error answer", replayed: []byte(eraFailure("0")), message: "rejected the replayed initialize"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compareInitialize(seen, tc.replayed)
			var failure *discoveryError
			require.True(t, errors.As(err, &failure), "a classified failure, got %v", err)
			assert.Equal(t, causeProtocolVersion, failure.cause)
			assert.Contains(t, err.Error(), tc.message)
		})
	}
}

func TestAnnouncements_FollowTheListChangedCapabilitiesTheHostSaw(t *testing.T) {
	const resources = `{"jsonrpc":"2.0","method":"notifications/resources/list_changed"}` + "\n"
	const prompts = `{"jsonrpc":"2.0","method":"notifications/prompts/list_changed"}` + "\n"
	cases := []struct {
		name         string
		capabilities string
		want         []string
	}{
		{name: "both", capabilities: `{"resources":{"listChanged":true},"prompts":{"listChanged":true},"tools":{"listChanged":true}}`, want: []string{resources, prompts}},
		{name: "resources only", capabilities: `{"resources":{"listChanged":true},"prompts":{"listChanged":false},"tools":{"listChanged":true}}`, want: []string{resources}},
		{name: "prompts only", capabilities: `{"resources":{"subscribe":true},"prompts":{"listChanged":true}}`, want: []string{prompts}},
		{name: "neither", capabilities: `{"tools":{"listChanged":true}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := announcements(resultLine(`{"protocolVersion":"2025-11-25","capabilities":` + tc.capabilities + `}`))
			assert.Equal(t, tc.want, lineStrings(lines))
		})
	}
}
