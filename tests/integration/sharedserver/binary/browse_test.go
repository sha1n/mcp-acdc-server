//go:build unix

package binary

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runBrowse runs the browse command of the binary to its exit and returns
// its stdout line, requiring exit code 0 and nothing else on stdout.
func runBrowse(t *testing.T, env *testkit.BinaryEnv, deadline time.Time, args ...string) string {
	t.Helper()
	l := env.Start(t, append([]string{"browse"}, args...)...)
	uiURL, err := l.ReadLine(deadline)
	require.NoError(t, err, "stderr:\n%s", l.Stderr())
	code, err := l.Wait(deadline)
	require.NoError(t, err)
	require.Equal(t, 0, code, "stderr:\n%s", l.Stderr())
	_, err = l.ReadLine(deadline)
	require.Error(t, err, "stdout must hold the URL line only")
	require.True(t, strings.HasPrefix(uiURL, "http://127.0.0.1:"), "unexpected URL %q", uiURL)
	return uiURL
}

func requireUIServed(t *testing.T, uiURL string) {
	t.Helper()
	resp, err := http.Get(uiURL) //nolint:gosec // the URL printed by browse
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestBinary_BrowseColdStartsASharedServerAndPrintsItsWebUIURL(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	env := testkit.NewBinaryEnv(t, acdcBinary)
	args := sharedLaunchArgs(testkit.CreateTestContentDir(t, nil), "--shared-server-linger", "1s")

	requireUIServed(t, runBrowse(t, env, deadline, args...))
}

func TestBinary_BrowseUsesTheSharedServerOfAnAttachedSession(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	env := testkit.NewBinaryEnv(t, acdcBinary)
	args := sharedLaunchArgs(testkit.CreateTestContentDir(t, nil), "--shared-server-linger", longLinger)
	launch := env.Start(t, args...)
	handshake(t, launch, deadline)
	session, logPath := awaitIsolatedAttach(t, env, launch, deadline)

	requireUIServed(t, runBrowse(t, env, deadline, append(args, "--ui-enabled", "--port", "1")...))

	runs, err := testkit.ReadServerRuns(logPath)
	require.NoError(t, err)
	require.Len(t, runs, 1, "browse must reach the running shared server")
	assert.Equal(t, []string{"attached:" + session}, runs[0].Sessions, "a browse logs no attach entry")
	endSession(t, launch, deadline)
}

func TestBinary_BrowseColdStartIgnoresAuthHostAndPortFromTheEnvironment(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	env := testkit.NewBinaryEnv(t, acdcBinary)
	env.Setenv("ACDC_MCP_AUTH_TYPE=basic", "ACDC_MCP_HOST=not a host", "ACDC_MCP_PORT=1")
	args := sharedLaunchArgs(testkit.CreateTestContentDir(t, nil), "--shared-server-linger", "1s")

	requireUIServed(t, runBrowse(t, env, deadline, append(args, "--auth-type", "apikey", "--port", "2")...))
}
