//go:build unix

package sessionproxy_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const proxyTestIdentity = "0123456789abcdef0123456789abcdef"

const proxyTestDeadline = 10 * time.Second

func fixedEndpointDir(dir string) func() (string, error) {
	return func() (string, error) { return dir, nil }
}

// unexpectedFallback fails a test whose session must never become a
// per-session server.
func unexpectedFallback(t *testing.T) func(context.Context, io.ReadCloser) error {
	return func(context.Context, io.ReadCloser) error {
		t.Error("the session proxy fell back to a per-session server")
		return errors.New("unexpected fallback")
	}
}

func proxyTestEndpointDir(t *testing.T) string {
	t.Helper()
	parent, err := os.MkdirTemp("/tmp", "acdc-proxy-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	return filepath.Join(parent, "ep")
}

// fakeSharedServer listens on the identity's socket once spawned and hands
// every accepted connection to the test.
type fakeSharedServer struct {
	t          *testing.T
	socketPath string
	spawns     atomic.Int32
	once       sync.Once
	accepted   chan net.Conn
}

func newFakeSharedServer(t *testing.T, endpointDir string) *fakeSharedServer {
	t.Helper()
	ep, err := endpoint.OpenEndpoint(endpointDir)
	require.NoError(t, err)
	socketPath, err := ep.SocketPath(proxyTestIdentity)
	require.NoError(t, err)
	return &fakeSharedServer{t: t, socketPath: socketPath, accepted: make(chan net.Conn, 4)}
}

func (s *fakeSharedServer) spawn(identity string) error {
	s.spawns.Add(1)
	if identity != proxyTestIdentity {
		return errors.New("unexpected identity " + identity)
	}
	s.once.Do(s.listen)
	return nil
}

func (s *fakeSharedServer) listen() {
	listener, err := net.Listen("unix", s.socketPath)
	if !assert.NoError(s.t, err) {
		return
	}
	var mu sync.Mutex
	var conns []net.Conn
	s.t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range conns {
			_ = conn.Close()
		}
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			s.accepted <- conn
		}
	}()
}

func (s *fakeSharedServer) nextConn() *fakeSharedConn {
	s.t.Helper()
	select {
	case conn := <-s.accepted:
		return &fakeSharedConn{t: s.t, conn: conn, lines: bufio.NewReader(conn)}
	case <-time.After(proxyTestDeadline):
		s.t.Fatal("the session proxy never connected")
		return nil
	}
}

type fakeSharedConn struct {
	t     *testing.T
	conn  net.Conn
	lines *bufio.Reader
}

func (c *fakeSharedConn) sendReady() {
	c.t.Helper()
	line, err := sharedserver.EncodeReadyLine("abcd1234", "/var/log/acdc.log")
	require.NoError(c.t, err)
	c.write(string(line))
}

func (c *fakeSharedConn) write(s string) {
	c.t.Helper()
	_, err := io.WriteString(c.conn, s)
	require.NoError(c.t, err)
}

func (c *fakeSharedConn) readLine() string {
	c.t.Helper()
	require.NoError(c.t, c.conn.SetReadDeadline(time.Now().Add(proxyTestDeadline)))
	line, err := c.lines.ReadString('\n')
	require.NoError(c.t, err)
	return line
}

func (c *fakeSharedConn) requireClosedByProxy() {
	c.t.Helper()
	require.NoError(c.t, c.conn.SetReadDeadline(time.Now().Add(proxyTestDeadline)))
	_, err := c.lines.ReadString('\n')
	require.ErrorIs(c.t, err, io.EOF)
}

type proxyHost struct {
	t      *testing.T
	stdin  *io.PipeWriter
	stdout *bufio.Reader
	cancel context.CancelFunc
	done   chan error
}

func startProxy(t *testing.T, cfg sessionproxy.Config) *proxyHost {
	t.Helper()
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	t.Cleanup(func() {
		_ = stdinWriter.Close()
		_ = stdoutReader.Close()
	})
	cfg.Stdin = stdinReader
	cfg.Stdout = stdoutWriter
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Identity == nil {
		cfg.Identity = func() (string, error) { return proxyTestIdentity, nil }
	}
	if cfg.ConnectWait == 0 {
		cfg.ConnectWait = proxyTestDeadline
	}
	if cfg.Fallback == nil {
		cfg.Fallback = unexpectedFallback(t)
	}

	ctx, cancel := context.WithCancel(context.Background())
	host := &proxyHost{t: t, stdin: stdinWriter, stdout: bufio.NewReader(stdoutReader), cancel: cancel, done: make(chan error, 1)}
	go func() { host.done <- sessionproxy.Run(ctx, cfg) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-host.done:
			if err != nil {
				t.Logf("session proxy ended: %v", err)
			}
		case <-time.After(proxyTestDeadline):
			t.Error("the session proxy did not return after cancellation")
		}
	})
	return host
}

func (h *proxyHost) write(s string) {
	h.t.Helper()
	_, err := io.WriteString(h.stdin, s)
	require.NoError(h.t, err)
}

func (h *proxyHost) readLine() string {
	h.t.Helper()
	read := make(chan string, 1)
	go func() {
		line, _ := h.stdout.ReadString('\n')
		read <- line
	}()
	select {
	case line := <-read:
		return line
	case <-time.After(proxyTestDeadline):
		h.t.Fatal("no line reached the host")
		return ""
	}
}

func (h *proxyHost) result() error {
	h.t.Helper()
	select {
	case err := <-h.done:
		h.done <- err
		return err
	case <-time.After(proxyTestDeadline):
		h.t.Fatal("the session proxy did not return")
		return nil
	}
}

func TestRun_RelaysHeldBytesInOrderBeforeLiveStdinAndHidesTheReadyLine(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	host := startProxy(t, sessionproxy.Config{EndpointDir: fixedEndpointDir(dir), Spawn: server.spawn})

	host.write("{\"id\":1}\n")
	host.write("{\"id\":2}\n")
	conn := server.nextConn()
	conn.sendReady()
	host.write("{\"id\":3}\n")

	assert.Equal(t, "{\"id\":1}\n", conn.readLine())
	assert.Equal(t, "{\"id\":2}\n", conn.readLine())
	assert.Equal(t, "{\"id\":3}\n", conn.readLine())
	conn.write("{\"id\":1,\"result\":{}}\n")
	assert.Equal(t, "{\"id\":1,\"result\":{}}\n", host.readLine(), "the ready line never reaches the host")
}

func TestRun_ForwardsLineBytesVerbatimInBothDirections(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	host := startProxy(t, sessionproxy.Config{EndpointDir: fixedEndpointDir(dir), Spawn: server.spawn})
	conn := server.nextConn()
	conn.sendReady()

	hostLines := []string{
		"{ \"jsonrpc\" : \"2.0\",\t\"id\":\"a\" , \"method\":\"ping\" }\n",
		"{not json\n",
		"\n",
	}
	for _, line := range hostLines {
		host.write(line)
	}
	for _, line := range hostLines {
		assert.Equal(t, line, conn.readLine())
	}

	serverLines := []string{
		"{\"jsonrpc\":\"2.0\",  \"id\":\"a\",\"result\":{\"text\":\"caf\\u00e9 café\"}}\n",
		"{\"jsonrpc\":\"2.0\",\"id\":null,\"error\":{\"code\":-32700,\"message\":\"parse error\"}}\n",
	}
	for _, line := range serverLines {
		conn.write(line)
	}
	for _, line := range serverLines {
		assert.Equal(t, line, host.readLine())
	}
}

func TestRun_AttachesToAListeningServerWithoutSpawning(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	server.once.Do(server.listen)
	host := startProxy(t, sessionproxy.Config{EndpointDir: fixedEndpointDir(dir), Spawn: server.spawn})

	conn := server.nextConn()
	conn.sendReady()
	host.write("{\"id\":1}\n")

	assert.Equal(t, "{\"id\":1}\n", conn.readLine())
	assert.Zero(t, server.spawns.Load())
}

func TestRun_KeepsSpawningUntilACandidateListens(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	spawn := func(identity string) error {
		if server.spawns.Load() < 3 {
			server.spawns.Add(1)
			return nil
		}
		return server.spawn(identity)
	}
	startProxy(t, sessionproxy.Config{EndpointDir: fixedEndpointDir(dir), Spawn: spawn})

	server.nextConn()
	assert.Equal(t, int32(4), server.spawns.Load())
}

func TestRun_StdinEOFEndsTheSession(t *testing.T) {
	tests := []struct {
		name  string
		reach func(*fakeSharedServer) *fakeSharedConn
	}{
		{name: "during discovery", reach: func(*fakeSharedServer) *fakeSharedConn { return nil }},
		{name: "during the readiness wait", reach: func(s *fakeSharedServer) *fakeSharedConn { return s.nextConn() }},
		{name: "after attach", reach: func(s *fakeSharedServer) *fakeSharedConn {
			conn := s.nextConn()
			conn.sendReady()
			conn.write("{\"id\":1,\"result\":{}}\n")
			return conn
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := proxyTestEndpointDir(t)
			server := newFakeSharedServer(t, dir)
			spawn := server.spawn
			if tt.name == "during discovery" {
				spawn = func(string) error { return nil }
			}
			host := startProxy(t, sessionproxy.Config{EndpointDir: fixedEndpointDir(dir), Spawn: spawn})
			conn := tt.reach(server)
			if tt.name == "after attach" {
				// Linux resets a socket closed with unread data, so the proxy
				// must have consumed the ready line before stdin ends.
				require.Equal(t, "{\"id\":1,\"result\":{}}\n", host.readLine(), "the session is relaying")
			}

			require.NoError(t, host.stdin.Close())

			require.NoError(t, host.result())
			if conn != nil {
				conn.requireClosedByProxy()
			}
		})
	}
}

func TestRun_CancellationEndsTheSessionWellInside500ms(t *testing.T) {
	tests := []struct {
		name  string
		reach func(*fakeSharedServer)
	}{
		{name: "during discovery", reach: func(*fakeSharedServer) {}},
		{name: "during the readiness wait", reach: func(s *fakeSharedServer) { s.nextConn() }},
		{name: "after attach", reach: func(s *fakeSharedServer) { s.nextConn().sendReady() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := proxyTestEndpointDir(t)
			server := newFakeSharedServer(t, dir)
			spawn := server.spawn
			if tt.name == "during discovery" {
				spawn = func(string) error { return nil }
			}
			host := startProxy(t, sessionproxy.Config{EndpointDir: fixedEndpointDir(dir), Spawn: spawn})
			tt.reach(server)

			cancelled := time.Now()
			host.cancel()

			require.NoError(t, host.result())
			assert.Less(t, time.Since(cancelled), 500*time.Millisecond)
		})
	}
}

func TestRun_FailsWhenTheConnectionDropsAfterAttach(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	host := startProxy(t, sessionproxy.Config{EndpointDir: fixedEndpointDir(dir), Spawn: server.spawn})
	conn := server.nextConn()
	conn.sendReady()

	require.NoError(t, conn.conn.Close())

	require.Error(t, host.result())
}
