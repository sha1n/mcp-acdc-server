//go:build unix

package sharedserver

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type attributedAttach struct {
	client     net.Conn
	serverConn *net.UnixConn
	logs       *syncLogBuffer
	logger     *slog.Logger
	server     *mcp.Server
	done       chan struct{}
}

func newAttributedAttach(t *testing.T) *attributedAttach {
	t.Helper()
	listener, err := net.Listen("unix", filepath.Join(shortTempDir(t), "attach.sock"))
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("failed to close listener: %v", err)
		}
	})
	client, err := net.Dial("unix", listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	serverConn, err := listener.Accept()
	require.NoError(t, err)
	require.NoError(t, client.SetDeadline(time.Now().Add(serveTestDeadline)))

	logs := &syncLogBuffer{}
	a := &attributedAttach{
		client:     client,
		serverConn: serverConn.(*net.UnixConn),
		logs:       logs,
		logger:     slog.New(newSessionHandler(slog.NewTextHandler(logs, nil))),
		server:     mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil),
		done:       make(chan struct{}),
	}
	return a
}

func (a *attributedAttach) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		defer close(a.done)
		attach(ctx, a.logger, a.serverConn, a.server, nil, "/var/log/acdc.log")
	}()
}

func (a *attributedAttach) awaitLogs(t *testing.T) string {
	t.Helper()
	select {
	case <-a.done:
	case <-time.After(serveTestDeadline):
		t.Fatal("attach did not return")
	}
	return a.logs.String()
}

func linesMatching(logs, pattern string) []string {
	re := regexp.MustCompile(pattern)
	var matched []string
	for _, line := range strings.Split(logs, "\n") {
		if re.MatchString(line) {
			matched = append(matched, line)
		}
	}
	return matched
}

func TestAttach_LogsAttachAndDetachAtInfoWithTheReadyLineSession(t *testing.T) {
	a := newAttributedAttach(t)
	a.start(t)
	reader := bufio.NewReader(a.client)
	line, err := reader.ReadBytes('\n')
	require.NoError(t, err)
	ready, err := DecodeReadyLine(line)
	require.NoError(t, err)

	_, err = io.WriteString(a.client, attachTestInitialize+"\n")
	require.NoError(t, err)
	_, err = reader.ReadBytes('\n')
	require.NoError(t, err)
	require.NoError(t, a.client.Close())
	logs := a.awaitLogs(t)

	session := regexp.QuoteMeta(ready.Session)
	assert.Len(t, linesMatching(logs, `^time=\S+ level=INFO msg="Session attached" session=`+session+`$`), 1, logs)
	assert.Len(t, linesMatching(logs, `^time=\S+ level=INFO msg="Session detached" session=`+session+`$`), 1, logs)
	assert.Len(t, linesMatching(logs, `msg="Session (attached|detached)"`), 2, logs)
	assert.Less(t, strings.Index(logs, "Session attached"), strings.Index(logs, "Session detached"))
}

func TestAttach_RequestHandlersLogWithTheSessionOfTheirConnection(t *testing.T) {
	a := newAttributedAttach(t)
	type echoArgs struct {
		Text string `json:"text"`
	}
	mcp.AddTool(a.server, &mcp.Tool{Name: "echo"}, func(ctx context.Context, _ *mcp.CallToolRequest, args echoArgs) (*mcp.CallToolResult, any, error) {
		a.logger.InfoContext(ctx, "Echo request", "text", args.Text)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: args.Text}}}, nil, nil
	})
	a.start(t)
	reader := bufio.NewReader(a.client)
	line, err := reader.ReadBytes('\n')
	require.NoError(t, err)
	ready, err := DecodeReadyLine(line)
	require.NoError(t, err)

	// Pipelined right behind the ready line: the request may be handled
	// before Connect has returned the session to attach.
	_, err = io.WriteString(a.client, strings.Join([]string{
		attachTestInitialize,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"text":"pipelined"}}}`,
		"",
	}, "\n"))
	require.NoError(t, err)
	for range 2 {
		_, err = reader.ReadBytes('\n')
		require.NoError(t, err)
	}
	require.NoError(t, a.client.Close())
	logs := a.awaitLogs(t)

	assert.Len(t, linesMatching(logs, `^time=\S+ level=INFO msg="Echo request" session=`+regexp.QuoteMeta(ready.Session)+` text=pipelined$`), 1, logs)
}

func TestAttach_ReadyLineFailureLogsAnErrorWithTheSessionAndNoAttachOrDetach(t *testing.T) {
	a := newAttributedAttach(t)
	require.NoError(t, a.serverConn.SetWriteDeadline(time.Now().Add(-time.Second)))
	a.start(t)

	logs := a.awaitLogs(t)

	assert.Len(t, linesMatching(logs, `^time=\S+ level=ERROR msg="Failed to send ready line" session=[0-9a-f]{8} error=`), 1, logs)
	assert.Empty(t, linesMatching(logs, `msg="Session (attached|detached)"`), logs)
}

func TestAttach_SessionEndedWithAnErrorCarriesTheSession(t *testing.T) {
	a := newAttributedAttach(t)
	a.start(t)
	reader := bufio.NewReader(a.client)
	line, err := reader.ReadBytes('\n')
	require.NoError(t, err)
	ready, err := DecodeReadyLine(line)
	require.NoError(t, err)

	_, err = io.WriteString(a.client, "{not json\n")
	require.NoError(t, err)
	logs := a.awaitLogs(t)

	session := regexp.QuoteMeta(ready.Session)
	assert.Len(t, linesMatching(logs, `^time=\S+ level=WARN msg="Session ended with an error" session=`+session+` error=`), 1, logs)
	assert.Len(t, linesMatching(logs, `^time=\S+ level=INFO msg="Session detached" session=`+session+`$`), 1, logs)
}

func TestAttach_CancellationBeforeTheReadyLineIsNotLogged(t *testing.T) {
	a := newAttributedAttach(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// Revalidation runs before the ready line, so cancelling there and waiting
	// for the server side to close guarantees the write fails by cancellation.
	cancelBeforeReady := func(context.Context) {
		cancel()
		_, err := io.ReadAll(a.client)
		assert.NoError(t, err)
	}
	go func() {
		defer close(a.done)
		attach(ctx, a.logger, a.serverConn, a.server, func(ctx context.Context) bool { cancelBeforeReady(ctx); return true }, "/var/log/acdc.log")
	}()

	logs := a.awaitLogs(t)

	assert.Empty(t, logs)
}

// attachRecord is what reached the handler: the session the entry's context
// carries and every session attribute a caller supplied, derived or not.
type attachRecord struct {
	message        string
	contextSession string
	callerSessions int
}

type recordingHandler struct {
	mu             *sync.Mutex
	records        *[]attachRecord
	callerSessions int
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(ctx context.Context, record slog.Record) error {
	callerSessions := h.callerSessions
	record.Attrs(func(a slog.Attr) bool {
		if a.Key == sessionAttrKey {
			callerSessions++
		}
		return true
	})
	session, _ := ctx.Value(sessionKey{}).(string)
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.records = append(*h.records, attachRecord{message: record.Message, contextSession: session, callerSessions: callerSessions})
	return nil
}

func (h *recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	derived := *h
	for _, a := range attrs {
		if a.Key == sessionAttrKey {
			derived.callerSessions++
		}
	}
	return &derived
}

func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

func TestAttach_LogsItsOwnEntriesWithTheSessionContextAndNoSessionAttribute(t *testing.T) {
	a := newAttributedAttach(t)
	var records []attachRecord
	mu := &sync.Mutex{}
	a.logger = slog.New(&recordingHandler{mu: mu, records: &records})
	a.start(t)
	reader := bufio.NewReader(a.client)
	line, err := reader.ReadBytes('\n')
	require.NoError(t, err)
	ready, err := DecodeReadyLine(line)
	require.NoError(t, err)

	_, err = io.WriteString(a.client, "{not json\n")
	require.NoError(t, err)
	a.awaitLogs(t)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []attachRecord{
		{message: "Session attached", contextSession: ready.Session},
		{message: "Session ended with an error", contextSession: ready.Session},
		{message: "Session detached", contextSession: ready.Session},
	}, records)
}

func TestAttach_ReadyLineFailureIsLoggedWithTheSessionContextAndNoSessionAttribute(t *testing.T) {
	a := newAttributedAttach(t)
	var records []attachRecord
	mu := &sync.Mutex{}
	a.logger = slog.New(&recordingHandler{mu: mu, records: &records})
	require.NoError(t, a.serverConn.SetWriteDeadline(time.Now().Add(-time.Second)))
	a.start(t)

	a.awaitLogs(t)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, records, 1)
	assert.Equal(t, "Failed to send ready line", records[0].message)
	assert.Regexp(t, `^[0-9a-f]{8}$`, records[0].contextSession)
	assert.Zero(t, records[0].callerSessions)
}

func requireEachEntryCarriesOneSession(t *testing.T, logs string) {
	t.Helper()
	lines := linesMatching(logs, `.`)
	require.NotEmpty(t, lines)
	for _, line := range lines {
		assert.Equal(t, 1, strings.Count(line, "session="), line)
	}
}

func TestAttach_EveryAttachDetachAndSessionErrorEntryCarriesTheSessionOnce(t *testing.T) {
	t.Run("attach, session error and detach", func(t *testing.T) {
		a := newAttributedAttach(t)
		a.start(t)
		_, err := bufio.NewReader(a.client).ReadBytes('\n')
		require.NoError(t, err)
		_, err = io.WriteString(a.client, "{not json\n")
		require.NoError(t, err)

		logs := a.awaitLogs(t)

		assert.Len(t, linesMatching(logs, `msg="Session (attached|ended with an error|detached)"`), 3, logs)
		requireEachEntryCarriesOneSession(t, logs)
	})
	t.Run("ready line failure", func(t *testing.T) {
		a := newAttributedAttach(t)
		require.NoError(t, a.serverConn.SetWriteDeadline(time.Now().Add(-time.Second)))
		a.start(t)

		logs := a.awaitLogs(t)

		assert.Len(t, linesMatching(logs, `msg="Failed to send ready line"`), 1, logs)
		requireEachEntryCarriesOneSession(t, logs)
	})
}

func TestAttach_PeerClosingBeforeTheReadyLineLogsNothing(t *testing.T) {
	a := newAttributedAttach(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// Revalidation runs before the ready line, so closing the peer there makes
	// the write fail by disconnection: EPIPE, or a reset on Linux.
	closePeerBeforeReady := func(context.Context) {
		assert.NoError(t, a.client.Close())
	}
	go func() {
		defer close(a.done)
		attach(ctx, a.logger, a.serverConn, a.server, func(ctx context.Context) bool { closePeerBeforeReady(ctx); return true }, "/var/log/acdc.log")
	}()

	logs := a.awaitLogs(t)

	require.NoError(t, ctx.Err())
	assert.Empty(t, logs)
}
