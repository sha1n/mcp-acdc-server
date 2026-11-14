//go:build unix

package app

import (
	"bufio"
	"context"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shortEndpointDir lives under /tmp because the per-test directory of
// t.TempDir can push a socket path past the darwin sun_path limit.
func shortEndpointDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "acdc-log-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func sharedServerLoggingFlags(t *testing.T, contentDir string) *pflag.FlagSet {
	t.Helper()
	flags := loggingDestinationFlags(t, contentDir)
	require.NoError(t, flags.Set("shared-server-linger", "1h"))
	return flags
}

// setLaunchIdentity marks the launch as the shared server of the identity its
// own settings and binary compute, so it passes the identity self-check.
func setLaunchIdentity(t *testing.T, params RunParams, flags *pflag.FlagSet, binary identity.Binary) string {
	t.Helper()
	settings, err := params.LoadSettings(flags)
	require.NoError(t, err)
	id, err := identity.Compute(settings, binary)
	require.NoError(t, err)
	require.NoError(t, flags.Set("shared-server-identity", id))
	return id
}

type loggingSharedServer struct {
	socketPath string
	logPath    string
	cancel     context.CancelFunc
	done       chan error
}

func startLoggingSharedServer(t *testing.T, params RunParams, flags *pflag.FlagSet) *loggingSharedServer {
	t.Helper()
	endpointDir := shortEndpointDir(t)
	logDir := t.TempDir()
	params.EndpointDir = func() (string, error) { return endpointDir, nil }
	params.LogDir = func() (string, error) { return logDir, nil }
	binary := identity.Binary{Version: "9.8.7"}
	id := setLaunchIdentity(t, params, flags, binary)
	ctx, cancel := context.WithCancel(context.Background())
	server := &loggingSharedServer{
		socketPath: filepath.Join(endpointDir, id+".sock"),
		logPath:    filepath.Join(logDir, id+".log"),
		cancel:     cancel,
		done:       make(chan error, 1),
	}
	go func() { server.done <- RunWithDeps(ctx, params, flags, binary) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-server.done:
			if err != nil {
				t.Logf("shared server stopped with: %v", err)
			}
			server.done <- err
		case <-time.After(loggingDestinationTestDeadline):
			t.Error("shared server did not stop")
		}
	})
	return server
}

// awaitReady returns once the build has completed: the ready line is only
// sent after it.
func (s *loggingSharedServer) awaitReady(t *testing.T) {
	t.Helper()
	awaitSharedServerSocket(t, s.socketPath)
	conn, err := net.Dial("unix", s.socketPath)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(loggingDestinationTestDeadline)))
	line, err := bufio.NewReader(conn).ReadString('\n')
	require.NoError(t, err)
	require.Contains(t, line, s.logPath)
}

func (s *loggingSharedServer) stop(t *testing.T) {
	t.Helper()
	s.cancel()
	select {
	case err := <-s.done:
		s.done <- err
		require.NoError(t, err)
	case <-time.After(loggingDestinationTestDeadline):
		t.Fatal("shared server did not stop")
	}
}

func (s *loggingSharedServer) logged(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(s.logPath)
	require.NoError(t, err)
	return string(content)
}

func TestRunWithDeps_SharedServerWritesStartupAndBuildEntriesOnlyToItsLogFile(t *testing.T) {
	stderr := captureOSStderr(t)
	params := DefaultRunParams()
	contentDir := writeLoggingDestinationContent(t, "Shared Guide")
	server := startLoggingSharedServer(t, params, sharedServerLoggingFlags(t, contentDir))

	server.awaitReady(t)
	server.stop(t)

	logged := server.logged(t)
	assert.Regexp(t, `(?m)^time=\S+ level=INFO msg="Starting MCP Acdc shared server" version=9\.8\.7 pid=\d+$`, logged)
	assert.Regexp(t, `(?m)^time=\S+ level=INFO msg="Config: content_dir" value=`+regexp.QuoteMeta(contentDir)+`$`, logged)
	assert.Regexp(t, loadedResourceEntry("Shared Guide"), logged)
	assert.Regexp(t, `(?m)^time=\S+ level=INFO msg="Registered tool" name=search$`, logged)
	assert.Empty(t, readCapturedFile(t, stderr))
}
