//go:build unix

package binary

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

// binaryTestBudget is the one absolute deadline of a binary-tier test.
const binaryTestBudget = 2 * time.Minute

type rpcLine struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// request sends a request and returns its response, skipping any other
// line the server writes first.
func request(t *testing.T, l *testkit.BinaryLaunch, deadline time.Time, id int, method, params string) rpcLine {
	t.Helper()
	require.NoError(t, l.Send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, id, method, params)))
	for {
		raw, err := l.ReadLine(deadline)
		require.NoError(t, err, "stderr:\n%s", l.Stderr())
		var line rpcLine
		require.NoError(t, json.Unmarshal([]byte(raw), &line), raw)
		if string(line.ID) == fmt.Sprint(id) {
			return line
		}
	}
}

// handshake initializes the session and proves it is served with a tool list.
func handshake(t *testing.T, l *testkit.BinaryLaunch, deadline time.Time) {
	t.Helper()
	initialized := request(t, l, deadline, 1, "initialize",
		`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"binary-tier","version":"1.0"}}`)
	require.Nil(t, initialized.Error)
	require.NoError(t, l.Send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	tools := request(t, l, deadline, 2, "tools/list", `{}`)
	require.Nil(t, tools.Error)
	require.Contains(t, string(tools.Result), `"search"`)
}

// endSession closes host stdin and requires the launch to exit with code 0.
func endSession(t *testing.T, l *testkit.BinaryLaunch, deadline time.Time) {
	t.Helper()
	require.NoError(t, l.CloseStdin())
	code, err := l.Wait(deadline)
	require.NoError(t, err)
	require.Equal(t, 0, code, "stderr:\n%s", l.Stderr())
}

// sharedConnectWait outlasts binaryTestBudget: a correct but slow server must
// not lose its session to a fallback, so the test deadline is the only budget.
const sharedConnectWait = "10m"

// sharedLaunchArgs are the arguments of a launch that attaches to a shared
// server; extra flags come later, so a test of the connect wait overrides it.
func sharedLaunchArgs(contentDir string, extra ...string) []string {
	return append([]string{"--content-dir", contentDir, "--transport", "stdio", "--shared-server-connect-wait", sharedConnectWait}, extra...)
}
