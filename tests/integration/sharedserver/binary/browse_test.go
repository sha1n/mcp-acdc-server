//go:build unix

package binary

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runBrowse runs the browse command of the binary with --no-open to its
// exit and returns its stdout line, requiring exit code 0 and nothing else
// on stdout.
func runBrowse(t *testing.T, env *testkit.BinaryEnv, deadline time.Time, args ...string) string {
	t.Helper()
	return runBrowseCommand(t, env, deadline, append(args, "--no-open")...)
}

func runBrowseCommand(t *testing.T, env *testkit.BinaryEnv, deadline time.Time, args ...string) string {
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

// fakeBrowserOpeners puts open and xdg-open first on the PATH of env; each
// appends the URL it is given to the returned file.
func fakeBrowserOpeners(t *testing.T, env *testkit.BinaryEnv) string {
	t.Helper()
	dir := filepath.Join(env.Root, "openers")
	require.NoError(t, os.Mkdir(dir, 0o700))
	opened := filepath.Join(dir, "opened")
	script := "#!/bin/sh\necho \"$1\" >> '" + opened + "'\n"
	for _, name := range []string{"open", "xdg-open"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700))
	}
	env.Setenv("PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"))
	return opened
}

func TestBinary_BrowseOpensTheBrowserOnThePrintedURL(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	env := testkit.NewBinaryEnv(t, acdcBinary)
	opened := fakeBrowserOpeners(t, env)
	args := sharedLaunchArgs(testkit.CreateTestContentDir(t, nil), "--shared-server-linger", "1s")

	uiURL := runBrowseCommand(t, env, deadline, args...)

	data, err := os.ReadFile(opened)
	require.NoError(t, err)
	assert.Equal(t, uiURL+"\n", string(data))
}

func TestBinary_BrowseNoOpenDoesNotOpenTheBrowser(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	env := testkit.NewBinaryEnv(t, acdcBinary)
	opened := fakeBrowserOpeners(t, env)
	args := sharedLaunchArgs(testkit.CreateTestContentDir(t, nil), "--shared-server-linger", "1s")

	requireUIServed(t, runBrowse(t, env, deadline, args...))

	assert.NoFileExists(t, opened)
}

func TestBinary_BrowseSucceedsWhenNoBrowserOpenerIsFound(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	env := testkit.NewBinaryEnv(t, acdcBinary)
	env.Setenv("PATH=" + filepath.Join(env.Root, "no-openers"))
	args := sharedLaunchArgs(testkit.CreateTestContentDir(t, nil), "--shared-server-linger", "1s")

	requireUIServed(t, runBrowseCommand(t, env, deadline, args...))
}

func TestBinary_BrowseClosesItsPipesWhileADescendantOfTheOpenerRuns(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	env := testkit.NewBinaryEnv(t, acdcBinary)
	dir := filepath.Join(env.Root, "openers")
	require.NoError(t, os.Mkdir(dir, 0o700))
	pidFile := filepath.Join(dir, "pid")
	// The opener leaves a long-lived descendant with the fds it inherited,
	// as a browser started by xdg-open does, and exits.
	script := "#!/bin/sh\nsleep 60 &\necho $! > '" + pidFile + "'\n"
	for _, name := range []string{"open", "xdg-open"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700))
	}
	env.Setenv("PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"))
	t.Cleanup(func() {
		if data, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	args := sharedLaunchArgs(testkit.CreateTestContentDir(t, nil), "--shared-server-linger", "1s")

	// runBrowseCommand waits for the exit, which the testkit reports only
	// once both captured pipes reach EOF.
	runBrowseCommand(t, env, time.Now().Add(min(10*time.Second, time.Until(deadline))), args...)

	require.FileExists(t, pidFile, "the opener ran")
}
