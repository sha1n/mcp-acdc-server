//go:build unix

package sharedserver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	relativeEndpointDir     = "acdc-relative-endpoint"
	rawSearchCallRequestID2 = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search","arguments":{"query":"authentication"}}}`
)

func TestSessionProxyFallback_PerSessionServerAnswersHeldRequestsInOrderForEveryCause(t *testing.T) {
	tests := []struct {
		name  string
		cause string
		args  []string
		// arrange runs before the launch and may replace the run parameters.
		arrange func(t *testing.T, h *testkit.SharedServerHarness, args []string, params *app.RunParams)
	}{
		{
			name:  "endpoint directory pre-created with mode 0755",
			cause: "endpoint",
			arrange: func(t *testing.T, h *testkit.SharedServerHarness, _ []string, _ *app.RunParams) {
				require.NoError(t, os.Mkdir(h.EndpointDir, 0o700))
				require.NoError(t, os.Chmod(h.EndpointDir, 0o755))
			},
		},
		{
			name:  "endpoint directory that is not an absolute path",
			cause: "endpoint",
			arrange: func(t *testing.T, _ *testkit.SharedServerHarness, _ []string, params *app.RunParams) {
				params.EndpointDir = func() (string, error) { return relativeEndpointDir, nil }
			},
		},
		{
			name:  "socket path over the length limit",
			cause: "socket_path",
			arrange: func(_ *testing.T, h *testkit.SharedServerHarness, _ []string, _ *app.RunParams) {
				h.EndpointDir = filepath.Join(h.EndpointDir, strings.Repeat("d", 100))
			},
		},
		{
			name:  "spawn seam returning an error",
			cause: "spawn",
			arrange: func(_ *testing.T, _ *testkit.SharedServerHarness, _ []string, params *app.RunParams) {
				params.SpawnSharedServer = func(string) error { return errors.New("spawn refused by the test") }
			},
		},
		{
			name:  "connect wait expiring against a test-held lock",
			cause: "connect_wait",
			args:  []string{"--shared-server-connect-wait", "300ms"},
			arrange: func(_ *testing.T, h *testkit.SharedServerHarness, args []string, _ *app.RunParams) {
				h.HoldLock(h.Identity(args))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := testkit.NewSharedServerHarness(t)
			args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t), tt.args...)
			params := h.SessionProxyParams(args)
			var spawns atomic.Int32
			tt.arrange(t, h, args, &params)
			spawn := params.SpawnSharedServer
			params.SpawnSharedServer = func(id string) error {
				spawns.Add(1)
				return spawn(id)
			}

			l, gate := testkit.StartHeldLaunch(t, params, h.Binary, args...)
			requireFallbackAnswersHeldRequests(t, l, gate, tt.cause)

			assert.Zero(t, h.Builds(), "no shared server ever served")
			if tt.cause == "spawn" {
				assert.Equal(t, int32(1), spawns.Load(), "a failed spawn is not retried")
			}
			_, err := os.Lstat(relativeEndpointDir)
			assert.True(t, os.IsNotExist(err), "nothing is created relative to the working directory: %v", err)
		})
	}
}

// requireFallbackAnswersHeldRequests writes the requests while discovery is
// held, so the proxy holds them, then lets discovery fail.
func requireFallbackAnswersHeldRequests(t *testing.T, l *testkit.LaunchUnderTest, gate *testkit.DiscoveryGate, cause string) {
	t.Helper()
	l.WriteLine(rawInitializeRequest)
	l.WriteLine(rawInitializedNotice)
	l.WriteLine(rawSearchCallRequestID2)
	require.Empty(t, l.FallbackWarnings(), "discovery is still held")

	gate.Open()

	initialize := decodeRawResponse(t, l.ReadLine())
	search := decodeRawResponse(t, l.ReadLine())
	assert.Equal(t, "1", string(initialize.ID), "initialize is answered first")
	assert.Nil(t, initialize.Error)
	assert.Contains(t, string(initialize.Result), `"serverInfo"`)
	assert.Equal(t, "2", string(search.ID))
	assert.Nil(t, search.Error)
	assert.Contains(t, string(search.Result), "Authentication Guide", "the search is answered from the content")
	assert.NotContains(t, string(search.Result), `"isError":true`)

	warnings := l.FallbackWarnings()
	require.Len(t, warnings, 1, "stderr: %s", l.Stderr())
	assert.Contains(t, warnings[0], "cause="+cause)
}
