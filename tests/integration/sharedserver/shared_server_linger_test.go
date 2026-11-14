//go:build unix

package sharedserver

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	lingerTestLinger   = time.Second
	lingerTestDeadline = 30 * time.Second
	lingerTestSlowTool = "linger_test_slow"
)

type lingerTestOptions struct {
	linger      string
	inMemory    bool
	buildGate   <-chan struct{}
	slowStarted chan<- struct{}
	identity    string
	endpointDir string
}

type lingerTestServer struct {
	endpointDir string
	identity    string
	launchedAt  time.Time
	cancel      context.CancelFunc
	done        chan error
}

// t.TempDir() is not used: its macOS paths exceed the 103-byte socket path budget.
func newLingerTestEndpointDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "acdc-lg-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newLingerTestIdentity(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}

func addLingerTestSlowTool(server *mcp.Server, started chan<- struct{}) {
	mcp.AddTool(server, &mcp.Tool{Name: lingerTestSlowTool, Description: "blocks until its request is cancelled"},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
			close(started)
			<-ctx.Done()
			return nil, struct{}{}, ctx.Err()
		})
}

func launchLingerTestServer(t *testing.T, opts lingerTestOptions) *lingerTestServer {
	t.Helper()
	s := &lingerTestServer{endpointDir: opts.endpointDir, identity: opts.identity, done: make(chan error, 1)}
	if s.endpointDir == "" {
		s.endpointDir = newLingerTestEndpointDir(t)
	}
	if s.identity == "" {
		s.identity = newLingerTestIdentity(t)
	}

	contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"linger.md": "---\nname: Linger Guide\ndescription: Guide for lingering\n---\nThis document covers lingering.",
		},
	})
	flags := testkit.NewTestFlags(t, contentDir, &testkit.FlagOptions{Transport: "stdio"})
	require.NoError(t, flags.Set("shared-server-identity", s.identity))
	require.NoError(t, flags.Set("shared-server-linger", opts.linger))
	require.NoError(t, flags.Set("search-in-memory", fmt.Sprintf("%t", opts.inMemory)))

	params := app.DefaultRunParams()
	createServer := params.CreateServer
	params.CreateServer = func(ctx context.Context, settings *config.Settings, version string) (app.Components, error) {
		if opts.buildGate != nil {
			<-opts.buildGate
		}
		components, err := createServer(ctx, settings, version)
		if err == nil && opts.slowStarted != nil {
			addLingerTestSlowTool(components.MCP, opts.slowStarted)
		}
		return components, err
	}
	params.EndpointDir = func() (string, error) { return s.endpointDir, nil }
	logDir := t.TempDir()
	params.LogDir = func() (string, error) { return logDir, nil }

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.launchedAt = time.Now()
	go func() { s.done <- app.RunWithDeps(ctx, params, flags, "test") }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-s.done:
			s.done <- err
		case <-time.After(lingerTestDeadline):
			t.Errorf("shared server did not stop")
		}
	})
	return s
}

func (s *lingerTestServer) socketPath() string {
	return filepath.Join(s.endpointDir, s.identity+".sock")
}

func (s *lingerTestServer) lockPath() string {
	return filepath.Join(s.endpointDir, s.identity+".lock")
}

func (s *lingerTestServer) awaitSocket(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(lingerTestDeadline)
	for {
		if info, err := os.Lstat(s.socketPath()); err == nil && info.Mode().Type() == fs.ModeSocket {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("socket %s was never bound", s.socketPath())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *lingerTestServer) awaitExit(t *testing.T) error {
	t.Helper()
	select {
	case err := <-s.done:
		s.done <- err
		return err
	case <-time.After(lingerTestDeadline):
		t.Fatalf("shared server did not exit")
		return nil
	}
}

func (s *lingerTestServer) assertRunningFor(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case err := <-s.done:
		s.done <- err
		t.Fatalf("shared server exited while a session was attached: %v", err)
	case <-time.After(d):
	}
}

func (s *lingerTestServer) assertSocketRemoved(t *testing.T) {
	t.Helper()
	_, err := os.Lstat(s.socketPath())
	assert.ErrorIs(t, err, fs.ErrNotExist, "the socket must be unlinked on linger shutdown")
}

func (s *lingerTestServer) assertLockReleased(t *testing.T) {
	t.Helper()
	lock := flock.New(s.lockPath())
	locked, err := lock.TryLock()
	require.NoError(t, err)
	assert.True(t, locked, "the lock must be released after the linger shutdown")
	_ = lock.Unlock()
}

type lingerTestConn struct {
	conn   net.Conn
	reader *bufio.Reader
}

func (s *lingerTestServer) dial(t *testing.T) *lingerTestConn {
	t.Helper()
	conn, err := net.Dial("unix", s.socketPath())
	require.NoError(t, err)
	require.NoError(t, conn.SetDeadline(time.Now().Add(lingerTestDeadline)))
	t.Cleanup(func() { _ = conn.Close() })
	return &lingerTestConn{conn: conn, reader: bufio.NewReader(conn)}
}

func (c *lingerTestConn) readMessage(t *testing.T) map[string]any {
	t.Helper()
	line, err := c.reader.ReadBytes('\n')
	require.NoError(t, err)
	var msg map[string]any
	require.NoError(t, json.Unmarshal(line, &msg), "line: %s", line)
	return msg
}

func (c *lingerTestConn) readReadyLine(t *testing.T) {
	t.Helper()
	ready := c.readMessage(t)
	require.Equal(t, true, ready["ready"], "first line must be the ready line: %v", ready)
}

func (c *lingerTestConn) send(t *testing.T, msg map[string]any) {
	t.Helper()
	b, err := json.Marshal(msg)
	require.NoError(t, err)
	_, err = fmt.Fprintf(c.conn, "%s\n", b)
	require.NoError(t, err)
}

func (c *lingerTestConn) initialize(t *testing.T) {
	t.Helper()
	c.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "linger-test-client", "version": "1.0"},
		},
	})
	for {
		msg := c.readMessage(t)
		if id, ok := msg["id"].(float64); ok && id == 1 {
			require.Nil(t, msg["error"], "initialize failed: %v", msg)
			break
		}
	}
	c.send(t, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

func (c *lingerTestConn) close(t *testing.T) time.Time {
	t.Helper()
	require.NoError(t, c.conn.Close())
	return time.Now()
}

func TestSharedServerLinger_WithNoConnectionsExitsAfterTheLingerAndRemovesTheSocket(t *testing.T) {
	server := launchLingerTestServer(t, lingerTestOptions{linger: "1s", inMemory: true})
	server.awaitSocket(t)

	require.NoError(t, server.awaitExit(t))

	assert.GreaterOrEqual(t, time.Since(server.launchedAt), lingerTestLinger, "exited before the linger elapsed")
	server.assertSocketRemoved(t)
	server.assertLockReleased(t)
}

func TestSharedServerLinger_AnOpenConnectionKeepsItRunningUntilALingerAfterTheClose(t *testing.T) {
	server := launchLingerTestServer(t, lingerTestOptions{linger: "1s", inMemory: true})
	server.awaitSocket(t)
	conn := server.dial(t)
	conn.readReadyLine(t)

	server.assertRunningFor(t, 2*lingerTestLinger)
	closedAt := conn.close(t)

	require.NoError(t, server.awaitExit(t))
	assert.GreaterOrEqual(t, time.Since(closedAt), lingerTestLinger, "exited before a linger after the last close")
	server.assertSocketRemoved(t)
	server.assertLockReleased(t)
}

func TestSharedServerLinger_AConnectionOpenDuringTheIndexBuildCountsAsAttached(t *testing.T) {
	gate := make(chan struct{})
	server := launchLingerTestServer(t, lingerTestOptions{linger: "1s", inMemory: true, buildGate: gate})
	server.awaitSocket(t)
	conn := server.dial(t)

	server.assertRunningFor(t, 2*lingerTestLinger)
	close(gate)
	conn.readReadyLine(t)
	closedAt := conn.close(t)

	require.NoError(t, server.awaitExit(t))
	assert.GreaterOrEqual(t, time.Since(closedAt), lingerTestLinger)
	server.assertSocketRemoved(t)
}

func TestSharedServerLinger_RemovesTheOnDiskIndexDirectory(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	indexDirs := func() []string {
		matches, err := filepath.Glob(filepath.Join(tempDir, "acdc_search_*"))
		require.NoError(t, err)
		return matches
	}
	server := launchLingerTestServer(t, lingerTestOptions{linger: "1s", inMemory: false})
	server.awaitSocket(t)
	conn := server.dial(t)
	conn.readReadyLine(t)
	require.Len(t, indexDirs(), 1, "search.in_memory=false must build the index on disk")

	conn.close(t)

	require.NoError(t, server.awaitExit(t))
	assert.Empty(t, indexDirs(), "the on-disk index directory must be removed on linger shutdown")
}

func TestSharedServerLinger_ASuccessorHoldingTheLockKeepsItsSocket(t *testing.T) {
	endpointDir := newLingerTestEndpointDir(t)
	identity := newLingerTestIdentity(t)
	first := launchLingerTestServer(t, lingerTestOptions{
		linger: "1s", inMemory: true, endpointDir: endpointDir, identity: identity,
	})
	first.awaitSocket(t)
	require.NoError(t, first.awaitExit(t))
	first.assertSocketRemoved(t)

	successor := launchLingerTestServer(t, lingerTestOptions{
		linger: "30s", inMemory: true, endpointDir: endpointDir, identity: identity,
	})
	successor.awaitSocket(t)
	conn := successor.dial(t)
	conn.readReadyLine(t)

	info, err := os.Lstat(successor.socketPath())
	require.NoError(t, err, "the successor's socket must survive its predecessor's shutdown")
	assert.Equal(t, fs.ModeSocket, info.Mode().Type())
}

func TestSharedServerLinger_CancellingTheSignalContextDropsConnectionsWithoutDrainOrLinger(t *testing.T) {
	started := make(chan struct{})
	server := launchLingerTestServer(t, lingerTestOptions{linger: "30s", inMemory: true, slowStarted: started})
	server.awaitSocket(t)
	conn := server.dial(t)
	conn.readReadyLine(t)
	conn.initialize(t)
	conn.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": lingerTestSlowTool, "arguments": map[string]any{}},
	})
	select {
	case <-started:
	case <-time.After(lingerTestDeadline):
		t.Fatal("the slow request never started")
	}

	cancelledAt := time.Now()
	server.cancel()

	select {
	case err := <-server.done:
		server.done <- err
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the shared server did not exit immediately on cancellation")
	}
	assert.Less(t, time.Since(cancelledAt), 30*time.Second, "the server must not wait for the linger")

	for {
		line, err := conn.reader.ReadBytes('\n')
		if err != nil {
			assert.ErrorIs(t, err, io.EOF, "the connection must simply drop")
			break
		}
		var msg map[string]any
		require.NoError(t, json.Unmarshal(line, &msg), "line: %s", line)
		id, isResponse := msg["id"].(float64)
		assert.False(t, isResponse && id == 2, "the in-flight request must get no response: %v", msg)
	}
}
