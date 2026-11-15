//go:build unix

package app

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// fallbackLaunch runs a session proxy launch whose discovery fails, against a
// per-session server that answers initialize, with host stdio as pipes.
func fallbackLaunch(t *testing.T, mutate func(*RunParams)) (stdin *io.PipeWriter, stdout *bufio.Reader, stderr *lockedBuffer, done <-chan error) {
	t.Helper()
	stderr = &lockedBuffer{}
	params := sessionProxyTestParams(t, sessionProxyTestSettings(t, "stdio", config.ServerModeShared), func(string) error { return nil })
	params.Stderr = stderr
	params.CreateServer = func(context.Context, *slog.Logger, *config.Settings, string) (Components, error) {
		return Components{MCP: mcp.NewServer(&mcp.Implementation{Name: "fallback-test", Version: "1.0.0"}, nil)}, nil
	}
	hostStdin, stdinWriter := io.Pipe()
	stdoutReader, hostStdout := io.Pipe()
	t.Cleanup(func() {
		_ = stdinWriter.Close()
		_ = stdoutReader.Close()
	})
	params.Stdin = hostStdin
	params.Stdout = hostStdout
	mutate(&params)

	result := make(chan error, 1)
	go func() { result <- RunWithDeps(context.Background(), params, nil, sessionProxyTestBinary) }()
	return stdinWriter, bufio.NewReader(stdoutReader), stderr, result
}

func TestRunWithDeps_SessionProxyFallsBackToAPerSessionServer(t *testing.T) {
	dirErr := errors.New("no home directory")
	spawnErr := errors.New("exec failed")
	tests := []struct {
		name    string
		mutate  func(*RunParams)
		wantErr error
	}{
		{name: "endpoint directory cannot be resolved", wantErr: dirErr, mutate: func(p *RunParams) {
			p.EndpointDir = func() (string, error) { return "", dirErr }
		}},
		{name: "spawn fails", wantErr: spawnErr, mutate: func(p *RunParams) {
			p.SpawnSharedServer = func(string) error { return spawnErr }
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdin, stdout, stderr, done := fallbackLaunch(t, tt.mutate)

			answered := make(chan string, 1)
			go func() {
				_, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"host","version":"1.0.0"}}}`+"\n")
				response, _ := stdout.ReadString('\n')
				answered <- response
			}()
			var response string
			select {
			case response = <-answered:
			case err := <-done:
				t.Fatalf("the launch ended before answering the host: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("the per-session server never answered initialize")
			}
			assert.Contains(t, response, `"id":1`)
			assert.Contains(t, response, `"result"`)
			require.NoError(t, stdin.Close())

			select {
			case err := <-done:
				require.NoError(t, err, "the launch never fails because of server mode")
			case <-time.After(10 * time.Second):
				t.Fatal("the fallback launch did not end on host stdin EOF")
			}
			warnings := 0
			for _, line := range strings.Split(stderr.String(), "\n") {
				if strings.Contains(line, "level=WARN") {
					warnings++
					assert.Contains(t, line, "per-session server")
					assert.Contains(t, line, tt.wantErr.Error())
				}
			}
			assert.Equal(t, 1, warnings, "exactly one warning is written: %s", stderr.String())
		})
	}
}

func TestRunWithDeps_SessionProxyGate(t *testing.T) {
	errSpawned := errors.New("spawn called")
	var spawns atomic.Int32
	spawn := func(string) error { spawns.Add(1); return errSpawned }
	tests := []struct {
		name        string
		transport   string
		mode        config.ServerMode
		spawn       func(string) error
		wantErr     error
		wantSpawned bool
	}{
		{name: "sse transport", transport: "sse", mode: config.ServerModeShared, spawn: spawn, wantErr: errSessionProxyTestCreate},
		{name: "session server mode", transport: "stdio", mode: config.ServerModeSession, spawn: spawn, wantErr: errSessionProxyTestCreate},
		{name: "nil spawn seam", transport: "stdio", mode: config.ServerModeShared, spawn: nil, wantErr: errSessionProxyTestCreate},
		// A failed spawn falls back to a per-session server, which reports the
		// startup error as today.
		{name: "stdio shared with spawn seam", transport: "stdio", mode: config.ServerModeShared, spawn: spawn, wantErr: errSessionProxyTestCreate, wantSpawned: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spawns.Store(0)
			params := sessionProxyTestParams(t, sessionProxyTestSettings(t, tt.transport, tt.mode), tt.spawn)

			err := RunWithDeps(context.Background(), params, nil, sessionProxyTestBinary)

			require.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, tt.wantSpawned, spawns.Load() > 0, "discovery is attempted only behind the gate")
		})
	}
}
