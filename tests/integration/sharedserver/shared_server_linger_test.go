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
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"syscall"
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
	gateBuild   bool
	slowStarted chan<- struct{}
	identity    string
	endpointDir string
}

type lingerTestServer struct {
	endpointDir string
	identity    string
	launchedAt  time.Time
	// exitedAt is written before Run's result is sent on done, so it is safe
	// to read after receiving from done. It is when Run returned, not when an
	// observer that may have been descheduled noticed.
	exitedAt time.Time
	cancel   context.CancelFunc
	done     chan error
	// buildGate is fresh per launch, so a retried attempt again proves that a
	// connection made while its own build is blocked counts as attached.
	buildGate chan struct{}
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
	if opts.gateBuild {
		s.buildGate = make(chan struct{})
	}
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
	params.CreateServer = func(ctx context.Context, logger *slog.Logger, settings *config.Settings, version string) (app.Components, error) {
		if s.buildGate != nil {
			select {
			case <-s.buildGate:
			case <-ctx.Done():
				return app.Components{}, ctx.Err()
			}
		}
		components, err := createServer(ctx, logger, settings, version)
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
	go func() {
		err := app.RunWithDeps(ctx, params, flags, "test")
		s.exitedAt = time.Now()
		s.done <- err
	}()
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

// launchWinningFirstLingerWindow exists because the linger starts at bind
// (spec section 6.2): a test that must observe the server or attach inside the
// first linger can legitimately lose that window to expiry when it is
// descheduled. A lost attempt is a valid outcome of the system, so it is
// verified as a clean linger exit and a fresh server is tried. act reports
// false only for a loss shaped like expiry and fails the test on anything else.
// Every blocking step of every attempt is bounded by the one deadline.
func launchWinningFirstLingerWindow(t *testing.T, opts lingerTestOptions, act func(s *lingerTestServer, deadline time.Time) bool) *lingerTestServer {
	t.Helper()
	deadline := time.Now().Add(lingerTestDeadline)
	for attempt := 1; ; attempt++ {
		requireWithinLingerWindowBudget(t, deadline, attempt)
		server := launchLingerTestServer(t, opts)
		if act(server, deadline) {
			requireWithinLingerWindowBudget(t, deadline, attempt)
			return server
		}
		server.assertCleanLingerExit(t, deadline)
		t.Logf("attempt %d lost its first linger window to expiry; retrying with a fresh server", attempt)
	}
}

func requireWithinLingerWindowBudget(t *testing.T, deadline time.Time, attempt int) {
	t.Helper()
	if time.Now().After(deadline) {
		t.Fatalf("no attempt won its first linger window within %s (%d attempts)", lingerTestDeadline, attempt)
	}
}

func isLingerExpiryDrop(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, fs.ErrNotExist) ||
		errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET)
}

func (s *lingerTestServer) assertCleanLingerExit(t *testing.T, deadline time.Time) {
	t.Helper()
	require.NoError(t, s.awaitExitBy(t, deadline), "a lost attempt must be a normal linger exit")
	require.GreaterOrEqual(t, s.exitedAt.Sub(s.launchedAt), lingerTestLinger, "a lost attempt exited before the linger elapsed")
	s.assertSocketRemoved(t)
	s.assertLockReleased(t)
}

func (s *lingerTestServer) exited() bool {
	select {
	case err := <-s.done:
		s.done <- err
		return true
	default:
		return false
	}
}

func (s *lingerTestServer) observeSocket(t *testing.T, deadline time.Time) bool {
	t.Helper()
	for {
		if info, err := os.Lstat(s.socketPath()); err == nil && info.Mode().Type() == fs.ModeSocket {
			return true
		}
		if s.exited() {
			return false
		}
		if time.Now().After(deadline) {
			t.Fatalf("socket %s was never bound", s.socketPath())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *lingerTestServer) tryDial(t *testing.T, deadline time.Time) (*lingerTestConn, bool) {
	t.Helper()
	dialer := net.Dialer{Deadline: deadline}
	conn, err := dialer.Dial("unix", s.socketPath())
	if err != nil {
		require.True(t, isLingerExpiryDrop(err), "dial failed for a reason other than expiry: %v", err)
		return nil, false
	}
	require.NoError(t, conn.SetDeadline(deadline))
	t.Cleanup(func() { _ = conn.Close() })
	return &lingerTestConn{conn: conn, reader: bufio.NewReader(conn)}, true
}

func (s *lingerTestServer) tryAttach(t *testing.T, deadline time.Time) (*lingerTestConn, bool) {
	t.Helper()
	if !s.observeSocket(t, deadline) {
		return nil, false
	}
	conn, ok := s.tryDial(t, deadline)
	if !ok || !conn.tryReadReadyLine(t) {
		return nil, false
	}
	return conn, true
}

// A dialed connection can still sit unaccepted in the listen backlog when the
// linger expires, so even after a successful dial the ready line can be lost
// to expiry.
func (c *lingerTestConn) tryReadReadyLine(t *testing.T) bool {
	t.Helper()
	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		require.True(t, isLingerExpiryDrop(err), "the connection failed for a reason other than expiry: %v", err)
		return false
	}
	var ready map[string]any
	require.NoError(t, json.Unmarshal(line, &ready), "line: %s", line)
	require.Equal(t, true, ready["ready"], "first line must be the ready line: %v", ready)
	return true
}

func (c *lingerTestConn) assertDroppedByExpiry(t *testing.T) {
	t.Helper()
	_, err := c.reader.ReadBytes('\n')
	require.Error(t, err, "a connection outliving the server must not receive anything")
	require.True(t, isLingerExpiryDrop(err), "the connection failed for a reason other than expiry: %v", err)
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
	return s.awaitExitBy(t, time.Now().Add(lingerTestDeadline))
}

func (s *lingerTestServer) awaitExitBy(t *testing.T, deadline time.Time) error {
	t.Helper()
	select {
	case err := <-s.done:
		s.done <- err
		return err
	case <-time.After(time.Until(deadline)):
		t.Fatalf("shared server did not exit")
		return nil
	}
}

func (s *lingerTestServer) runsFor(t *testing.T, d time.Duration, deadline time.Time) bool {
	t.Helper()
	if time.Until(deadline) < d {
		t.Fatalf("the remaining first linger window budget is shorter than %s", d)
	}
	select {
	case err := <-s.done:
		s.done <- err
		return false
	case <-time.After(d):
		// A descheduled observer can find both ready; select would then pick
		// either, so an exit that already happened must win over the timer.
		return !s.exited()
	}
}

func (s *lingerTestServer) assertRunningFor(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case err := <-s.done:
		s.done <- err
		t.Fatalf("shared server exited while a session was attached: %v", err)
	case <-time.After(d):
		if s.exited() {
			t.Fatalf("shared server exited while a session was attached: %v", s.awaitExit(t))
		}
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
	// Taken before the close: the server may see the close, and start its
	// linger, before Close returns here.
	closedAt := time.Now()
	require.NoError(t, c.conn.Close())
	return closedAt
}

func TestSharedServerLinger_WithNoConnectionsExitsAfterTheLingerAndRemovesTheSocket(t *testing.T) {
	server := launchWinningFirstLingerWindow(t, lingerTestOptions{linger: "1s", inMemory: true},
		func(s *lingerTestServer, deadline time.Time) bool { return s.observeSocket(t, deadline) })

	require.NoError(t, server.awaitExit(t))

	assert.GreaterOrEqual(t, server.exitedAt.Sub(server.launchedAt), lingerTestLinger, "exited before the linger elapsed")
	server.assertSocketRemoved(t)
	server.assertLockReleased(t)
}

func TestSharedServerLinger_AnOpenConnectionKeepsItRunningUntilALingerAfterTheClose(t *testing.T) {
	var conn *lingerTestConn
	server := launchWinningFirstLingerWindow(t, lingerTestOptions{linger: "1s", inMemory: true},
		func(s *lingerTestServer, deadline time.Time) (attached bool) {
			conn, attached = s.tryAttach(t, deadline)
			return attached
		})

	server.assertRunningFor(t, 2*lingerTestLinger)
	closedAt := conn.close(t)

	require.NoError(t, server.awaitExit(t))
	assert.GreaterOrEqual(t, server.exitedAt.Sub(closedAt), lingerTestLinger, "exited before a linger after the last close")
	server.assertSocketRemoved(t)
	server.assertLockReleased(t)
}

func TestSharedServerLinger_AConnectionOpenDuringTheIndexBuildCountsAsAttached(t *testing.T) {
	var conn *lingerTestConn
	server := launchWinningFirstLingerWindow(t, lingerTestOptions{linger: "1s", inMemory: true, gateBuild: true},
		func(s *lingerTestServer, deadline time.Time) bool {
			if !s.observeSocket(t, deadline) {
				return false
			}
			dialed, ok := s.tryDial(t, deadline)
			if !ok {
				return false
			}
			if !s.runsFor(t, 2*lingerTestLinger, deadline) {
				dialed.assertDroppedByExpiry(t)
				return false
			}
			close(s.buildGate)
			if !dialed.tryReadReadyLine(t) {
				return false
			}
			conn = dialed
			return true
		})
	closedAt := conn.close(t)

	require.NoError(t, server.awaitExit(t))
	assert.GreaterOrEqual(t, server.exitedAt.Sub(closedAt), lingerTestLinger)
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
	var conn *lingerTestConn
	server := launchWinningFirstLingerWindow(t, lingerTestOptions{linger: "1s", inMemory: false},
		func(s *lingerTestServer, deadline time.Time) (attached bool) {
			conn, attached = s.tryAttach(t, deadline)
			return attached
		})
	require.Len(t, indexDirs(), 1, "search.in_memory=false must build the index on disk")

	conn.close(t)

	require.NoError(t, server.awaitExit(t))
	assert.Empty(t, indexDirs(), "the on-disk index directory must be removed on linger shutdown")
}

func TestSharedServerLinger_ASuccessorHoldingTheLockKeepsItsSocket(t *testing.T) {
	first := launchWinningFirstLingerWindow(t, lingerTestOptions{linger: "1s", inMemory: true},
		func(s *lingerTestServer, deadline time.Time) bool { return s.observeSocket(t, deadline) })
	require.NoError(t, first.awaitExit(t))
	first.assertSocketRemoved(t)

	successor := launchLingerTestServer(t, lingerTestOptions{
		linger: "1h", inMemory: true, endpointDir: first.endpointDir, identity: first.identity,
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
	server := launchLingerTestServer(t, lingerTestOptions{linger: "1h", inMemory: true, slowStarted: started})
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
