package main

import (
	"io"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)
	orig := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = orig })

	fn()

	require.NoError(t, w.Close())
	os.Stdout = orig
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

func TestExecute_HelpListsServerModeFlagsWithoutShortForms(t *testing.T) {
	out := captureStdout(t, func() {
		require.NoError(t, Execute("test", "test", "acdc-mcp", []string{"--help"}))
	})

	for _, name := range []string{"server-mode", "shared-server-connect-wait", "shared-server-linger"} {
		longOnly := regexp.MustCompile(`(?m)^\s+--` + regexp.QuoteMeta(name) + `\s`)
		assert.Regexp(t, longOnly, out, "--%s must appear in --help without a short form", name)
	}
}

func TestExecute_InvalidServerModeSettingsFailEveryLaunch(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"unknown mode stdio", []string{"--server-mode", "strict"}, "server_mode"},
		{"unknown mode sse", []string{"--transport", "sse", "--server-mode", "strict"}, "server_mode"},
		{"zero connect wait session", []string{"--server-mode", "session", "--shared-server-connect-wait", "0s"}, "shared_server.connect_wait"},
		{"short linger sse session", []string{"--transport", "sse", "--server-mode", "session", "--shared-server-linger", "999ms"}, "shared_server.linger"},
		{"bare number linger", []string{"--shared-server-linger", "5"}, "Go duration"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())

			err := Execute("test", "test", "acdc-mcp", tt.args)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
