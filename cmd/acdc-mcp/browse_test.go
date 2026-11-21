package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecute_BrowseRejectsTheSSETransport(t *testing.T) {
	t.Chdir(t.TempDir())

	err := Execute("test", "test", "acdc-mcp", []string{"browse", "--transport", "sse"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sse")
}

func TestExecute_BrowseTakesTheSettingsFlagsAndRejectsUnknownOnes(t *testing.T) {
	t.Chdir(t.TempDir())

	err := Execute("test", "test", "acdc-mcp", []string{"browse", "--unknown-flag"})
	require.Error(t, err)

	// An invalid setting fails settings validation, so it was parsed.
	err = Execute("test", "test", "acdc-mcp", []string{"browse", "--ui-enabled", "-H", "h", "-p", "1", "--ui-max-results", "0"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ui.max_results")
}

func TestExecute_BrowseIsListedInHelp(t *testing.T) {
	out := captureStdout(t, func() {
		require.NoError(t, Execute("test", "test", "acdc-mcp", []string{"--help"}))
	})

	assert.Contains(t, out, "browse")
	assert.NotContains(t, out, "completion")
}

func TestExecute_BrowseHelpListsNoOpen(t *testing.T) {
	out := captureStdout(t, func() {
		require.NoError(t, Execute("test", "test", "acdc-mcp", []string{"browse", "--help"}))
	})

	assert.Contains(t, out, "--no-open")
	assert.Contains(t, out, "opens the browser")
	assert.Contains(t, out, "no attached sessions and no viewers")
}

func TestExecute_RootCommandRejectsNoOpen(t *testing.T) {
	t.Chdir(t.TempDir())

	err := Execute("test", "test", "acdc-mcp", []string{"--no-open"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-open")
}
