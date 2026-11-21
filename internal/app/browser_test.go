//go:build unix

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrowserCommand_PerPlatform(t *testing.T) {
	tests := []struct {
		goos string
		want string
	}{
		{goos: "darwin", want: "open"},
		{goos: "linux", want: "xdg-open"},
		{goos: "freebsd", want: "xdg-open"},
		{goos: "windows", want: ""},
		{goos: "plan9", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			assert.Equal(t, tt.want, browserCommand(tt.goos))
		})
	}
}

func TestBrowserOpener_FailsOnAPlatformWithoutAnOpener(t *testing.T) {
	err := browserOpener("windows", time.Second)("http://x/")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "windows")
}

func TestOpenWith_RunsTheOpenerOnTheURL(t *testing.T) {
	dir := t.TempDir()
	opened := filepath.Join(dir, "opened")
	script := filepath.Join(dir, "opener")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho \"$1\" > '"+opened+"'\n"), 0o700))

	err := openWith(script, "http://127.0.0.1:1/ui/", 5*time.Second)

	require.NoError(t, err)
	data, err := os.ReadFile(opened)
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:1/ui/\n", string(data))
}

func TestOpenWith_Fails(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		{name: "missing opener", command: filepath.Join(t.TempDir(), "no-such-opener")},
		{name: "opener error", command: "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := openWith(tt.command, "http://x/", 5*time.Second)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.command)
		})
	}
}

func TestOpenWith_ReportsAnOpenerStillRunningAfterTheWaitAsUnconfirmed(t *testing.T) {
	start := time.Now()

	err := openWith("sleep", "10", 50*time.Millisecond)

	require.ErrorIs(t, err, errBrowserOpenUnconfirmed)
	assert.Contains(t, err.Error(), "sleep")
	assert.Less(t, time.Since(start), 5*time.Second)
}
