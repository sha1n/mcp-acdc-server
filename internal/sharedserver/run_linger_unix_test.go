//go:build unix

package sharedserver

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_RejectsALingerBelowTheMinimumBeforeCreatingAnything(t *testing.T) {
	tests := []struct {
		name    string
		linger  time.Duration
		wantErr string
	}{
		{name: "zero", linger: 0, wantErr: "shared server linger must be at least 1s, got: 0s"},
		{name: "negative", linger: -time.Second, wantErr: "shared server linger must be at least 1s, got: -1s"},
		{name: "just below the minimum", linger: config.MinSharedServerLinger - time.Nanosecond, wantErr: "shared server linger must be at least 1s, got: 999.999999ms"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := shortTempDir(t)
			endpointDir := filepath.Join(root, "ep")
			logDir := filepath.Join(root, "logs")
			require.NoError(t, os.Mkdir(logDir, 0o700))
			build, built := buildSpy(nil)
			startupLogged := false

			err := Run(context.Background(), Config{
				Identity:    testIdentity,
				EndpointDir: endpointDir,
				LogDir:      logDir,
				LogStartup:  func(*slog.Logger) { startupLogged = true },
				Build:       build,
				Linger:      tt.linger,
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.NotErrorIs(t, err, ErrLockNotAcquired, "a rejected linger is not a lost lock and must not exit silently")
			assert.NoDirExists(t, endpointDir, "no endpoint directory, lock or socket may be created")
			logEntries, readErr := os.ReadDir(logDir)
			require.NoError(t, readErr)
			assert.Empty(t, logEntries, "no log file may be opened for a rejected linger")
			assert.False(t, startupLogged, "nothing may be logged for a rejected linger")
			assert.False(t, *built)
		})
	}
}

func TestRun_AcceptsTheMinimumLinger(t *testing.T) {
	root := shortTempDir(t)
	endpointDir := filepath.Join(root, "ep")
	logDir := filepath.Join(root, "logs")
	require.NoError(t, os.Mkdir(logDir, 0o700))
	build, built := buildSpy(nil)
	startedAt := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Config{
			Identity:    testIdentity,
			EndpointDir: endpointDir,
			LogDir:      logDir,
			Build:       build,
			Linger:      config.MinSharedServerLinger,
		})
	}()

	err := awaitRunResult(t, done)

	require.NoError(t, err, "the minimum linger must be served and expire normally")
	assert.True(t, *built)
	assert.GreaterOrEqual(t, time.Since(startedAt), config.MinSharedServerLinger)
	assert.FileExists(t, filepath.Join(logDir, testIdentity+".log"))
	assertLockReleased(t, endpointDir)
}
