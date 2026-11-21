//go:build unix

package sharedserver

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireBrowseURL requires a browse that succeeded with nothing but its
// URL on stdout, and returns that URL.
func requireBrowseURL(t *testing.T, r testkit.BrowseResult) string {
	t.Helper()
	require.NoError(t, r.Err, "stderr: %s", r.Stderr)
	uiURL := strings.TrimSuffix(r.Stdout, "\n")
	require.Equal(t, uiURL+"\n", r.Stdout, "stdout must hold the URL line only")
	require.True(t, strings.HasPrefix(uiURL, "http://127.0.0.1:"), "unexpected URL %q", uiURL)
	return uiURL
}

func requireServesUI(t *testing.T, uiURL string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testkit.SharedServerDeadline)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uiURL, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func sharedServerLog(t *testing.T, h *testkit.SharedServerHarness, args []string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(h.LogDir, h.Identity(args)+".log"))
	require.NoError(t, err)
	return string(data)
}

func TestBrowse_ColdStartStartsASharedServerAndPrintsItsWebUIURL(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t))

	uiURL := requireBrowseURL(t, h.Browse(args...))

	requireServesUI(t, uiURL)
	assert.Equal(t, 1, h.Builds())
	assert.Contains(t, sharedServerLog(t, h, args), "Web UI started")
}

func TestBrowse_UsesTheRunningSharedServerOfAnAttachedSession(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t))
	connectAndListTools(t, h.Launch(args...))
	spawns := h.Spawns()

	// Ignored flags do not change the identity browse looks for.
	first := requireBrowseURL(t, h.Browse(append(args, "--ui-enabled", "--host", "h", "--port", "1", "--auth-type", "basic")...))
	second := requireBrowseURL(t, h.Browse(args...))

	assert.Equal(t, first, second, "every request gets the URL of the running Web UI")
	requireServesUI(t, first)
	assert.Equal(t, spawns, h.Spawns(), "browse must not spawn while a shared server runs")
	assert.Equal(t, 1, h.Builds())
}

func TestBrowse_ABindFailureFailsTheBrowseAndTheNextBrowseTriesAgain(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	h.FailUIBinds(1)
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t))
	l := h.Launch(args...)
	session := connectAndListTools(t, l)

	failed := h.Browse(args...)

	require.Error(t, failed.Err)
	assert.Contains(t, failed.Err.Error(), "address already in use")
	assert.Empty(t, failed.Stdout)
	ctx, cancel := context.WithTimeout(context.Background(), testkit.SharedServerDeadline)
	defer cancel()
	_, err := session.ListTools(ctx, nil)
	require.NoError(t, err, "an attached session is unaffected by the bind failure")

	requireServesUI(t, requireBrowseURL(t, h.Browse(args...)))
	assert.Equal(t, 1, h.Builds())
}

func TestBrowse_IsNotAnAttachedSessionAndDoesNotKeepTheSharedServerAlive(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t))

	requireBrowseURL(t, h.Browse(args...))

	h.Eventually(func() bool { return len(h.SharedServerExits()) == 1 },
		"the shared server must stop after its linger with only a browse served")
	runs, err := testkit.ReadServerRuns(filepath.Join(h.LogDir, h.Identity(args)+".log"))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Empty(t, runs[0].Sessions, "a browse logs no attach entry")
	assert.NotContains(t, sharedServerLog(t, h, args), "Session attached")
}

func TestBrowse_FailsWithoutAPerSessionFallbackWhenNoSharedServerCanBeReached(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t), "--shared-server-connect-wait", "300ms")
	release := h.HoldLock(h.Identity(args))
	defer release()

	r := h.Browse(args...)

	require.Error(t, r.Err)
	assert.Contains(t, r.Err.Error(), "connect wait")
	assert.Empty(t, r.Stdout)
	assert.Equal(t, 0, h.Builds())
}
