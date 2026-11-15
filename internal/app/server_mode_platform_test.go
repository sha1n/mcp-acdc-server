package app

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type platformLogEntry struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

func newPlatformDebugLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func platformLogEntries(t *testing.T, buf *bytes.Buffer) []platformLogEntry {
	t.Helper()
	var entries []platformLogEntry
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var entry platformLogEntry
		require.NoError(t, json.Unmarshal(line, &entry))
		entries = append(entries, entry)
	}
	return entries
}

func assertSingleDebugEntry(t *testing.T, buf *bytes.Buffer) {
	t.Helper()
	entries := platformLogEntries(t, buf)
	require.Len(t, entries, 1)
	assert.Equal(t, slog.LevelDebug.String(), entries[0].Level)
	assert.NotEmpty(t, entries[0].Msg)
}

var bothServerModes = []config.ServerMode{config.ServerModeShared, config.ServerModeSession}

func assertWindowsLogForConfiguredMode(t *testing.T, mode config.ServerMode, buf *bytes.Buffer) {
	t.Helper()
	if mode == config.ServerModeShared {
		assertSingleDebugEntry(t, buf)
		return
	}
	assert.Empty(t, platformLogEntries(t, buf))
}

func TestEffectiveServerMode_WindowsBehavesAsSessionAndLogsOnlyAnOverriddenShared(t *testing.T) {
	for _, mode := range bothServerModes {
		t.Run(string(mode), func(t *testing.T) {
			logger, buf := newPlatformDebugLogger()

			effective := effectiveServerMode(logger, "windows", mode)

			assert.Equal(t, config.ServerModeSession, effective)
			assertWindowsLogForConfiguredMode(t, mode, buf)
		})
	}
}

func TestEffectiveServerMode_OtherPlatformsKeepTheConfiguredModeSilently(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		for _, mode := range bothServerModes {
			t.Run(goos+"/"+string(mode), func(t *testing.T) {
				logger, buf := newPlatformDebugLogger()

				effective := effectiveServerMode(logger, goos, mode)

				assert.Equal(t, mode, effective)
				assert.Empty(t, platformLogEntries(t, buf))
			})
		}
	}
}

func platformSettings(transport string, mode config.ServerMode) *config.Settings {
	return &config.Settings{Transport: transport, ServerMode: mode}
}

func TestAttemptsDiscovery_NeverOnWindows(t *testing.T) {
	for _, mode := range bothServerModes {
		t.Run(string(mode), func(t *testing.T) {
			logger, buf := newPlatformDebugLogger()

			attempted := attemptsDiscovery(logger, "windows", platformSettings("stdio", mode))

			assert.False(t, attempted)
			assertWindowsLogForConfiguredMode(t, mode, buf)
		})
	}
}

func TestAttemptsDiscovery_SSEIsInertOnEveryPlatform(t *testing.T) {
	for _, goos := range []string{"windows", "linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			logger, buf := newPlatformDebugLogger()

			attempted := attemptsDiscovery(logger, goos, platformSettings("sse", config.ServerModeShared))

			assert.False(t, attempted)
			assert.Empty(t, platformLogEntries(t, buf))
		})
	}
}

func TestAttemptsDiscovery_UnixFollowsTheConfiguredMode(t *testing.T) {
	tests := []struct {
		name string
		mode config.ServerMode
		want bool
	}{
		{name: "shared", mode: config.ServerModeShared, want: true},
		{name: "session", mode: config.ServerModeSession, want: false},
	}
	for _, goos := range []string{"linux", "darwin"} {
		for _, tt := range tests {
			t.Run(goos+"/"+tt.name, func(t *testing.T) {
				logger, buf := newPlatformDebugLogger()

				attempted := attemptsDiscovery(logger, goos, platformSettings("stdio", tt.mode))

				assert.Equal(t, tt.want, attempted)
				assert.Empty(t, platformLogEntries(t, buf))
			})
		}
	}
}
