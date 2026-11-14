package sharedserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

const (
	listenID = "2"

	methodAcknowledged         = "notifications/subscriptions/acknowledged"
	methodResourcesListChanged = "notifications/resources/list_changed"
	metaKeySubscriptionID      = "io.modelcontextprotocol/subscriptionId"

	// awaitTimeout is generous because delivery depends on the catalog
	// refresher's 2s debounce plus the SDK's own list-changed coalescing; a
	// timeout signals a missing message, not scheduling jitter.
	awaitTimeout = 15 * time.Second
	pollInterval = 250 * time.Millisecond
	stopTimeout  = 5 * time.Second
	writeTimeout = 5 * time.Second
)

const modernMeta = `"_meta":{` +
	`"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
	`"io.modelcontextprotocol/clientCapabilities":{},` +
	`"io.modelcontextprotocol/clientInfo":{"name":"listen-probe","version":"1.0.0"}}`

// handshakeLines are the modern-era opening exchange a host sends before it
// opens a listen stream; a session proxy replays them verbatim on recovery.
var handshakeLines = []string{
	`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{` + modernMeta + `}}`,
}

var listenLine = `{"jsonrpc":"2.0","id":` + listenID + `,"method":"subscriptions/listen","params":{` +
	modernMeta + `,"notifications":{"resourcesListChanged":true}}}`

// TestListenProbe_ReplayedListenIsAcknowledgedAndDelivers guards the go-sdk
// behaviour that spec section 8.5 relies on: a session proxy re-sends a
// cached subscriptions/listen request verbatim, same id, to a fresh shared
// server after replaying the handshake, and expects that stream to be live.
func TestListenProbe_ReplayedListenIsAcknowledgedAndDelivers(t *testing.T) {
	contentDir := newContentDir(t)

	original := startRawServer(t, contentDir)
	original.sendAll(t, handshakeLines...)
	original.send(t, listenLine)
	requireAcknowledged(t, original.awaitListenOutcome(t))
	original.stop(t)

	fresh := startRawServer(t, contentDir)
	fresh.sendAll(t, handshakeLines...)
	fresh.send(t, listenLine)
	requireAcknowledged(t, fresh.awaitListenOutcome(t))

	writeContentFile(t, contentDir, "docs/added.md", "# Added\n\nA document added after the listen stream was replayed.\n")
	fresh.awaitListChangedDelivery(t)
}

func requireAcknowledged(t *testing.T, msg rpcMessage) {
	t.Helper()
	require.Nil(t, msg.Error, "listen request answered with an error instead of being acknowledged: %s", msg.raw)
	require.Equal(t, methodAcknowledged, msg.Method, "unexpected first listen-stream message: %s", msg.raw)
	require.Equal(t, listenID, msg.subscriptionID(), "acknowledgement not stamped with the listen id: %s", msg.raw)
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Error  json.RawMessage `json:"error"`
	Params struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	} `json:"params"`
	raw string
}

func (m rpcMessage) subscriptionID() string {
	return string(m.Params.Meta[metaKeySubscriptionID])
}

func (m rpcMessage) answers(id string) bool {
	return string(m.ID) == id && m.Method == ""
}

// rawServer talks to the server over net.Pipe rather than io.Pipe because
// net.Pipe honours write deadlines: a server that stops reading must fail the
// write, not hang the test goroutine past every other bound.
type rawServer struct {
	conn       net.Conn
	serverConn net.Conn
	lines      chan rpcMessage
	done       chan struct{}
	session    *mcp.ServerSession
	cleanup    func()
	stopped    bool
	nextID     int
}

func startRawServer(t *testing.T, contentDir string) *rawServer {
	t.Helper()

	flags := testkit.NewTestFlags(t, contentDir, &testkit.FlagOptions{Transport: "stdio"})
	settings, err := config.LoadSettingsWithFlags(flags)
	require.NoError(t, err)

	components, err := app.CreateServer(context.Background(), settings, "probe")
	require.NoError(t, err)

	clientConn, serverConn := net.Pipe()
	s := &rawServer{
		conn:       clientConn,
		serverConn: serverConn,
		lines:      make(chan rpcMessage, 64),
		done:       make(chan struct{}),
		cleanup:    components.Cleanup,
		nextID:     100,
	}
	t.Cleanup(func() { s.stop(t) })

	session, err := components.MCP.Connect(context.Background(), &mcp.IOTransport{Reader: serverConn, Writer: serverConn}, nil)
	require.NoError(t, err)
	s.session = session
	go s.readLines()
	return s
}

func (s *rawServer) readLines() {
	defer close(s.lines)
	scanner := bufio.NewScanner(s.conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var msg rpcMessage
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			continue
		}
		msg.raw = scanner.Text()
		select {
		case s.lines <- msg:
		case <-s.done:
			return
		}
	}
}

func (s *rawServer) send(t *testing.T, line string) {
	t.Helper()
	require.NoError(t, s.conn.SetWriteDeadline(time.Now().Add(writeTimeout)))
	_, err := io.WriteString(s.conn, line+"\n")
	require.NoError(t, err, "server did not consume input within %s", writeTimeout)
}

func (s *rawServer) sendAll(t *testing.T, lines ...string) {
	t.Helper()
	for _, line := range lines {
		s.send(t, line)
	}
}

func (s *rawServer) next(t *testing.T, deadline <-chan time.Time) rpcMessage {
	t.Helper()
	select {
	case msg, ok := <-s.lines:
		require.True(t, ok, "server output closed")
		return msg
	case <-deadline:
		require.FailNow(t, "timed out waiting for server output")
		return rpcMessage{}
	}
}

func (s *rawServer) awaitListenOutcome(t *testing.T) rpcMessage {
	t.Helper()
	deadline := time.After(awaitTimeout)
	for {
		msg := s.next(t, deadline)
		if msg.Method == methodAcknowledged || msg.answers(listenID) {
			return msg
		}
	}
}

// awaitListChangedDelivery keeps issuing search calls, because the catalog is
// revalidated only on demand, until a resources list-changed notification
// stamped with the listen id arrives on the stream.
func (s *rawServer) awaitListChangedDelivery(t *testing.T) {
	t.Helper()
	deadline := time.After(awaitTimeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	s.sendSearch(t)
	for {
		select {
		case msg, ok := <-s.lines:
			require.True(t, ok, "server output closed before list-changed delivery")
			require.False(t, msg.answers(listenID), "listen stream ended before list-changed delivery: %s", msg.raw)
			if msg.Method == methodResourcesListChanged {
				require.Equal(t, listenID, msg.subscriptionID(), "list-changed not stamped with the listen id: %s", msg.raw)
				return
			}
		case <-ticker.C:
			s.sendSearch(t)
		case <-deadline:
			require.FailNow(t, "no resources list-changed notification delivered on the replayed listen stream")
		}
	}
}

func (s *rawServer) sendSearch(t *testing.T) {
	t.Helper()
	s.nextID++
	s.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{%s,"name":"search","arguments":{"query":"added"}}}`,
		s.nextID, modernMeta))
}

func (s *rawServer) stop(t *testing.T) {
	t.Helper()
	if s.stopped {
		return
	}
	s.stopped = true
	defer s.cleanup()

	close(s.done)
	// Both pipe ends close before any wait so a handler that never drains
	// cannot hold the transport open; the session wait below is then bounded.
	_ = s.conn.Close()
	_ = s.serverConn.Close()
	if s.session != nil {
		s.awaitSessionEnd(t)
	}
}

func (s *rawServer) awaitSessionEnd(t *testing.T) {
	t.Helper()
	ended := make(chan struct{})
	go func() {
		_ = s.session.Wait()
		close(ended)
	}()
	select {
	case <-ended:
	case <-time.After(stopTimeout):
		t.Errorf("server session did not end within %s of its transport closing", stopTimeout)
	}
}

func newContentDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeContentFile(t, dir, "README.md", "# Listen Probe\n\nFixture for the listen-stream probe.\n")
	writeContentFile(t, dir, "docs/a.md", "# Guide A\n\nInitial document.\n")
	return dir
}

func writeContentFile(t *testing.T, root, relativePath, content string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(relativePath))
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte(content), 0o644))
}
