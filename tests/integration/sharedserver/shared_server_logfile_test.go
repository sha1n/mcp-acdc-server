//go:build unix

package sharedserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// launchWithLogFileTestDir mirrors launch, with the log directory seam supplied by the
// test and the flag set open to extra settings.
func (s *sharedServerUnderTest) launchWithLogFileTestDir(t *testing.T, logDir func() (string, error), configure func(*pflag.FlagSet)) {
	t.Helper()
	contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"auth-guide.md": "---\nname: Authentication Guide\ndescription: Guide for authentication\n---\nThis document covers authentication.",
		},
	})
	flags := testkit.NewTestFlags(t, contentDir, &testkit.FlagOptions{Transport: "stdio"})
	require.NoError(t, flags.Set("shared-server-identity", s.identity))
	if configure != nil {
		configure(flags)
	}

	params := app.DefaultRunParams()
	params.EndpointDir = func() (string, error) { return s.endpointDir, nil }
	params.LogDir = logDir

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go func() { s.done <- app.RunWithDeps(ctx, params, flags, "test") }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-s.done:
			if err != nil {
				t.Logf("shared server stopped with: %v", err)
			}
		case <-time.After(sharedServerSocketTestDeadline):
			t.Errorf("shared server did not stop")
		}
	})
}

func (s *sharedServerUnderTest) awaitLogFileTestExit(t *testing.T) error {
	t.Helper()
	select {
	case err := <-s.done:
		s.done <- err
		return err
	case <-time.After(sharedServerSocketTestDeadline):
		t.Fatal("shared server did not exit")
		return nil
	}
}

func logFileTestDir(dir string) func() (string, error) {
	return func() (string, error) { return dir, nil }
}

func readLogFileTestContent(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(content)
}

func TestSharedServerLogFile_ReadyLineCarriesTheAbsoluteLogPath(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), "state", "acdc-mcp")
	server := newSharedServerUnderTest(t, newSharedServerSocketTestEndpointDir(t))
	server.launchWithLogFileTestDir(t, logFileTestDir(logDir), nil)

	ready := server.dial(t).readLine(t)

	logPath := filepath.Join(logDir, server.identity+".log")
	assert.Equal(t, logPath, ready["log"])
	dirInfo, err := os.Stat(logDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
	fileInfo, err := os.Stat(logPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())
	entries, err := os.ReadDir(server.endpointDir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotEqual(t, ".log", filepath.Ext(entry.Name()), "the log must not live in the endpoint directory")
	}
}

func TestSharedServerLogFile_UnusableLogDirectoryStillServesWithAnEmptyLog(t *testing.T) {
	tests := []struct {
		name   string
		logDir func(t *testing.T) func() (string, error)
	}{
		{
			name: "unwritable parent",
			logDir: func(t *testing.T) func() (string, error) {
				parent := filepath.Join(t.TempDir(), "readonly")
				require.NoError(t, os.Mkdir(parent, 0o500))
				t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
				return logFileTestDir(filepath.Join(parent, "acdc-mcp"))
			},
		},
		{
			name: "relative directory",
			logDir: func(t *testing.T) func() (string, error) {
				return logFileTestDir(filepath.Join("state", "acdc-mcp"))
			},
		},
		{
			name: "resolution failure",
			logDir: func(t *testing.T) func() (string, error) {
				return func() (string, error) { return "", errors.New("no home directory") }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cwd := t.TempDir()
			t.Chdir(cwd)
			server := newSharedServerUnderTest(t, newSharedServerSocketTestEndpointDir(t))
			server.launchWithLogFileTestDir(t, tt.logDir(t), nil)

			client := server.dial(t)
			ready := client.readLine(t)
			assert.Equal(t, "", ready["log"])

			client.send(t, map[string]any{
				"jsonrpc": "2.0", "id": 1, "method": "initialize",
				"params": map[string]any{
					"protocolVersion": "2025-06-18",
					"capabilities":    map[string]any{},
					"clientInfo":      map[string]any{"name": "raw-line-client", "version": "1.0"},
				},
			})
			initialized := client.responseTo(t, 1)
			require.Nil(t, initialized["error"], "initialize failed: %v", initialized)
			require.NotNil(t, initialized["result"])
			entries, err := os.ReadDir(cwd)
			require.NoError(t, err)
			assert.Empty(t, entries, "nothing may be written relative to the working directory")
		})
	}
}

func TestSharedServerLogFile_LostLockCreatesAndAppendsNothing(t *testing.T) {
	tests := []struct {
		name     string
		existing bool
	}{
		{name: "no log yet", existing: false},
		{name: "log of an earlier shared server", existing: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logDir := filepath.Join(t.TempDir(), "acdc-mcp")
			server := newSharedServerUnderTest(t, newSharedServerSocketTestEndpointDir(t))
			logPath := filepath.Join(logDir, server.identity+".log")
			if tt.existing {
				require.NoError(t, os.Mkdir(logDir, 0o700))
				require.NoError(t, os.WriteFile(logPath, []byte("earlier\n"), 0o600))
			}
			held := flock.New(server.lockPath())
			locked, err := held.TryLock()
			require.NoError(t, err)
			require.True(t, locked)
			t.Cleanup(func() { _ = held.Unlock() })

			server.launchWithLogFileTestDir(t, logFileTestDir(logDir), nil)

			require.NoError(t, server.awaitLogFileTestExit(t))
			if !tt.existing {
				assert.NoDirExists(t, logDir, "a candidate that loses the lock creates no log")
				return
			}
			assert.Equal(t, "earlier\n", readLogFileTestContent(t, logPath))
			entries, err := os.ReadDir(logDir)
			require.NoError(t, err)
			assert.Len(t, entries, 1)
		})
	}
}

func TestSharedServerLogFile_StartupEntryCarriesThePIDAndARestartAppends(t *testing.T) {
	logDir := t.TempDir()
	endpointDir := newSharedServerSocketTestEndpointDir(t)
	first := newSharedServerUnderTest(t, endpointDir)
	first.launchWithLogFileTestDir(t, logFileTestDir(logDir), nil)
	first.dial(t).readLine(t)
	require.NoError(t, first.stop(t))

	logPath := filepath.Join(logDir, first.identity+".log")
	afterFirst := readLogFileTestContent(t, logPath)

	second := newSharedServerUnderTest(t, endpointDir)
	second.identity = first.identity
	second.launchWithLogFileTestDir(t, logFileTestDir(logDir), nil)
	second.dial(t).readLine(t)
	require.NoError(t, second.stop(t))

	content := readLogFileTestContent(t, logPath)
	assert.Truef(t, len(content) > len(afterFirst) && content[:len(afterFirst)] == afterFirst,
		"the second start must append to the first one's log:\n%s", content)
	startup := regexp.MustCompile(`(?m)^time=\S+ level=INFO msg="Starting MCP Acdc shared server" .*\bpid=(\d+)\b`)
	matches := startup.FindAllStringSubmatch(content, -1)
	require.Len(t, matches, 2, "one startup entry per start:\n%s", content)
	for _, match := range matches {
		assert.Equal(t, strconv.Itoa(os.Getpid()), match[1])
	}
}

func TestSharedServerLogFile_OmitsAuthUITransportHostAndPort(t *testing.T) {
	logDir := t.TempDir()
	server := newSharedServerUnderTest(t, newSharedServerSocketTestEndpointDir(t))
	server.launchWithLogFileTestDir(t, logFileTestDir(logDir), func(flags *pflag.FlagSet) {
		require.NoError(t, flags.Set("auth-type", "basic"))
		require.NoError(t, flags.Set("auth-basic-username", "log-test-user"))
		require.NoError(t, flags.Set("auth-basic-password", "log-test-password"))
		require.NoError(t, flags.Set("ui-enabled", "true"))
	})
	server.dial(t).readLine(t)
	require.NoError(t, server.stop(t))

	content := readLogFileTestContent(t, filepath.Join(logDir, server.identity+".log"))

	assert.Contains(t, content, `msg="Config: content_dir"`)
	assert.Contains(t, content, `msg="Config: search.max_results"`)
	for _, forbidden := range []string{
		"Config: transport", "Config: host", "Config: port", "Config: auth", "Config: ui.",
		"Config: server_mode", "Config: shared_server", "Web UI requires the sse transport",
		"log-test-user", "log-test-password", "count=",
	} {
		assert.NotContains(t, content, forbidden)
	}
}
