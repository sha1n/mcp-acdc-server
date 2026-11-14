//go:build unix

package sharedserver

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sharedServerSocketTestDeadline = 30 * time.Second

type sharedServerUnderTest struct {
	endpointDir string
	identity    string
	builds      *atomic.Int32
	cancel      context.CancelFunc
	done        chan error
}

func (s *sharedServerUnderTest) socketPath() string {
	return filepath.Join(s.endpointDir, s.identity+".sock")
}

func (s *sharedServerUnderTest) lockPath() string {
	return filepath.Join(s.endpointDir, s.identity+".lock")
}

// t.TempDir() is not used: its macOS paths exceed the 103-byte socket path budget.
func newSharedServerSocketTestEndpointDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "acdc-ss-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newSharedServerSocketTestIdentity(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}

func newSharedServerUnderTest(t *testing.T, endpointDir string) *sharedServerUnderTest {
	t.Helper()
	return &sharedServerUnderTest{
		endpointDir: endpointDir,
		identity:    newSharedServerSocketTestIdentity(t),
		builds:      &atomic.Int32{},
		done:        make(chan error, 1),
	}
}

// launch runs the shared server path of RunWithDeps in a goroutine, as the
// in-process tier of the test strategy prescribes.
func (s *sharedServerUnderTest) launch(t *testing.T) {
	t.Helper()
	contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"auth-guide.md": "---\nname: Authentication Guide\ndescription: Guide for authentication\n---\nThis document covers authentication and security best practices.",
		},
	})
	flags := testkit.NewTestFlags(t, contentDir, &testkit.FlagOptions{Transport: "stdio"})
	require.NoError(t, flags.Set("shared-server-identity", s.identity))

	params := app.DefaultRunParams()
	createServer := params.CreateServer
	params.CreateServer = func(ctx context.Context, settings *config.Settings, version string) (app.Components, error) {
		s.builds.Add(1)
		return createServer(ctx, settings, version)
	}
	params.EndpointDir = func() (string, error) { return s.endpointDir, nil }

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

func (s *sharedServerUnderTest) stop(t *testing.T) error {
	t.Helper()
	s.cancel()
	select {
	case err := <-s.done:
		s.done <- err
		return err
	case <-time.After(sharedServerSocketTestDeadline):
		t.Fatalf("shared server did not stop")
		return nil
	}
}

type sharedServerLineClient struct {
	conn   net.Conn
	reader *bufio.Reader
}

func (s *sharedServerUnderTest) dial(t *testing.T) *sharedServerLineClient {
	t.Helper()
	deadline := time.Now().Add(sharedServerSocketTestDeadline)
	for {
		conn, err := net.Dial("unix", s.socketPath())
		if err == nil {
			require.NoError(t, conn.SetDeadline(time.Now().Add(sharedServerSocketTestDeadline)))
			t.Cleanup(func() { _ = conn.Close() })
			return &sharedServerLineClient{conn: conn, reader: bufio.NewReader(conn)}
		}
		select {
		case err := <-s.done:
			t.Fatalf("shared server exited before accepting: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("socket %s never accepted a connection: %v", s.socketPath(), err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (c *sharedServerLineClient) readLine(t *testing.T) map[string]any {
	t.Helper()
	line, err := c.reader.ReadBytes('\n')
	require.NoError(t, err)
	var msg map[string]any
	require.NoError(t, json.Unmarshal(line, &msg), "line: %s", line)
	return msg
}

func (c *sharedServerLineClient) readReadyLine(t *testing.T) map[string]any {
	t.Helper()
	ready := c.readLine(t)
	require.Equal(t, true, ready["ready"], "first line must be the ready line: %v", ready)
	require.Len(t, ready, 3, "ready line carries exactly ready, session and log: %v", ready)
	assert.Regexp(t, `^[0-9a-f]{8}$`, ready["session"])
	assert.Equal(t, "", ready["log"])
	return ready
}

func (c *sharedServerLineClient) send(t *testing.T, msg map[string]any) {
	t.Helper()
	b, err := json.Marshal(msg)
	require.NoError(t, err)
	_, err = fmt.Fprintf(c.conn, "%s\n", b)
	require.NoError(t, err)
}

func (c *sharedServerLineClient) responseTo(t *testing.T, id float64) map[string]any {
	t.Helper()
	for {
		msg := c.readLine(t)
		if msgID, ok := msg["id"].(float64); ok && msgID == id {
			return msg
		}
	}
}

func TestSharedServer_ServesInitializeAndSearchAfterTheReadyLine(t *testing.T) {
	server := newSharedServerUnderTest(t, newSharedServerSocketTestEndpointDir(t))
	server.launch(t)

	client := server.dial(t)
	client.readReadyLine(t)

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

	client.send(t, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	client.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "search", "arguments": map[string]any{"query": "authentication"}},
	})
	searched := client.responseTo(t, 2)
	require.Nil(t, searched["error"], "search failed: %v", searched)
	result, ok := searched["result"].(map[string]any)
	require.True(t, ok, "search result: %v", searched)
	assert.NotEqual(t, true, result["isError"], "search result: %v", result)
	assert.NotEmpty(t, result["content"])
	assert.Equal(t, int32(1), server.builds.Load(), "one catalog and index per shared server")
}

func TestSharedServer_ConcurrentConnectionsGetDistinctSessions(t *testing.T) {
	server := newSharedServerUnderTest(t, newSharedServerSocketTestEndpointDir(t))
	server.launch(t)

	first := server.dial(t)
	second := server.dial(t)

	firstReady := first.readReadyLine(t)
	secondReady := second.readReadyLine(t)

	assert.NotEqual(t, firstReady["session"], secondReady["session"])
	assert.Equal(t, int32(1), server.builds.Load())
}

func TestSharedServer_LostLockExitsWithoutBindingOrWriting(t *testing.T) {
	endpointDir := newSharedServerSocketTestEndpointDir(t)
	server := newSharedServerUnderTest(t, endpointDir)
	held := flock.New(server.lockPath())
	locked, err := held.TryLock()
	require.NoError(t, err)
	require.True(t, locked)
	t.Cleanup(func() { _ = held.Unlock() })

	server.launch(t)

	select {
	case err := <-server.done:
		assert.NoError(t, err, "losing the lock is not an error to report")
		server.done <- err
	case <-time.After(sharedServerSocketTestDeadline):
		t.Fatal("shared server did not exit after losing the lock")
	}

	_, err = os.Lstat(server.socketPath())
	assert.True(t, errors.Is(err, fs.ErrNotExist), "socket must not be created: %v", err)
	assert.Equal(t, int32(0), server.builds.Load(), "no catalog or index without the lock")
	entries, err := os.ReadDir(endpointDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, server.identity+".lock", entries[0].Name())
}

func TestSharedServer_ReplacesAStaleSocketFile(t *testing.T) {
	server := newSharedServerUnderTest(t, newSharedServerSocketTestEndpointDir(t))
	require.NoError(t, os.WriteFile(server.socketPath(), []byte("stale"), 0o600))

	server.launch(t)

	client := server.dial(t)
	client.readReadyLine(t)
	info, err := os.Lstat(server.socketPath())
	require.NoError(t, err)
	assert.Equal(t, fs.ModeSocket, info.Mode().Type())
}

func TestSharedServer_ClosingTheListenerKeepsTheSocketPath(t *testing.T) {
	server := newSharedServerUnderTest(t, newSharedServerSocketTestEndpointDir(t))
	server.launch(t)
	client := server.dial(t)
	client.readReadyLine(t)

	require.NoError(t, server.stop(t))

	info, err := os.Lstat(server.socketPath())
	require.NoError(t, err, "the socket path must survive the listener close")
	assert.Equal(t, fs.ModeSocket, info.Mode().Type())
}
