//go:build unix

package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var sessionProxyTestBinary = identity.Binary{Version: "1.0.0-test", Build: "test"}

var errSessionProxyTestCreate = errors.New("create server called")

func sessionProxyTestSettings(t *testing.T, transport string, mode config.ServerMode) *config.Settings {
	t.Helper()
	return &config.Settings{
		ContentDir:   t.TempDir(),
		Transport:    transport,
		ServerMode:   mode,
		SharedServer: config.SharedServerSettings{ConnectWait: time.Minute},
	}
}

// t.TempDir() is not used: its macOS paths exceed the socket path budget.
func sessionProxyTestEndpointDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "acdc-app-")
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("failed to remove %s: %v", dir, err)
		}
	})
	return dir
}

// sessionProxyTestStdin never ends, so only discovery or the spawn seam can
// end the session.
func sessionProxyTestStdin(t *testing.T) io.Reader {
	t.Helper()
	stdin, hostWriter := io.Pipe()
	t.Cleanup(func() {
		if err := hostWriter.Close(); err != nil {
			t.Errorf("failed to close host stdin: %v", err)
		}
	})
	return stdin
}

func sessionProxyTestParams(t *testing.T, settings *config.Settings, spawn func(string) error) RunParams {
	t.Helper()
	endpointDir := sessionProxyTestEndpointDir(t)
	return RunParams{
		Stderr: io.Discard,
		LoadSettings: func(*pflag.FlagSet) (*config.Settings, error) {
			return settings, nil
		},
		ValidSettings: noopValidate,
		CreateServer: func(context.Context, *slog.Logger, *config.Settings, string) (Components, error) {
			return Components{}, errSessionProxyTestCreate
		},
		EndpointDir:       func() (string, error) { return endpointDir, nil },
		SpawnSharedServer: spawn,
		Stdin:             sessionProxyTestStdin(t),
		Stdout:            io.Discard,
	}
}

func TestRunWithDeps_SessionProxyRejectsMissingDependencies(t *testing.T) {
	spawn := func(string) error { return nil }
	tests := []struct {
		name   string
		mutate func(*RunParams)
	}{
		{name: "nil endpoint dir", mutate: func(p *RunParams) { p.EndpointDir = nil }},
		{name: "nil stdin", mutate: func(p *RunParams) { p.Stdin = nil }},
		{name: "nil stdout", mutate: func(p *RunParams) { p.Stdout = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := sessionProxyTestParams(t, sessionProxyTestSettings(t, "stdio", config.ServerModeShared), spawn)
			tt.mutate(&params)

			err := RunWithDeps(context.Background(), params, nil, sessionProxyTestBinary)

			require.Error(t, err)
			assert.NotErrorIs(t, err, errSessionProxyTestCreate)
		})
	}
}

func TestRunWithDeps_SessionProxyWrapsTheEndpointDirError(t *testing.T) {
	dirErr := errors.New("no home directory")
	params := sessionProxyTestParams(t, sessionProxyTestSettings(t, "stdio", config.ServerModeShared), func(string) error { return nil })
	params.EndpointDir = func() (string, error) { return "", dirErr }

	err := RunWithDeps(context.Background(), params, nil, sessionProxyTestBinary)

	require.ErrorIs(t, err, dirErr)
}

func TestRunWithDeps_SessionProxyWrapsTheSpawnError(t *testing.T) {
	spawnErr := errors.New("exec failed")
	params := sessionProxyTestParams(t, sessionProxyTestSettings(t, "stdio", config.ServerModeShared), func(string) error { return spawnErr })

	err := RunWithDeps(context.Background(), params, nil, sessionProxyTestBinary)

	require.ErrorIs(t, err, spawnErr)
}

func TestRunWithDeps_SessionProxyGate(t *testing.T) {
	errSpawned := errors.New("spawn called")
	spawn := func(string) error { return errSpawned }
	tests := []struct {
		name      string
		transport string
		mode      config.ServerMode
		spawn     func(string) error
		wantErr   error
	}{
		{name: "sse transport", transport: "sse", mode: config.ServerModeShared, spawn: spawn, wantErr: errSessionProxyTestCreate},
		{name: "session server mode", transport: "stdio", mode: config.ServerModeSession, spawn: spawn, wantErr: errSessionProxyTestCreate},
		{name: "nil spawn seam", transport: "stdio", mode: config.ServerModeShared, spawn: nil, wantErr: errSessionProxyTestCreate},
		{name: "stdio shared with spawn seam", transport: "stdio", mode: config.ServerModeShared, spawn: spawn, wantErr: errSpawned},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := sessionProxyTestParams(t, sessionProxyTestSettings(t, tt.transport, tt.mode), tt.spawn)

			err := RunWithDeps(context.Background(), params, nil, sessionProxyTestBinary)

			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
