//go:build unix

package sharedserver

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const attachTestInitialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"attach-test","version":"1.0.0"}}}`

type attachUnderTest struct {
	client net.Conn
	reader *bufio.Reader
	cancel context.CancelFunc
	logs   *bytes.Buffer
	done   chan struct{}
	ready  ReadyLine
}

func startAttach(t *testing.T) *attachUnderTest {
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

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a := &attachUnderTest{
		client: client,
		reader: bufio.NewReader(client),
		cancel: cancel,
		logs:   &bytes.Buffer{},
		done:   make(chan struct{}),
	}
	logger := slog.New(newSessionHandler(slog.NewTextHandler(a.logs, nil)))
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	go func() {
		defer close(a.done)
		attach(ctx, logger, serverConn.(*net.UnixConn), server, nil, "/var/log/acdc.log")
	}()

	require.NoError(t, client.SetDeadline(time.Now().Add(serveTestDeadline)))
	a.ready, err = DecodeReadyLine(a.readLine(t))
	require.NoError(t, err)
	return a
}

func (a *attachUnderTest) readLine(t *testing.T) []byte {
	t.Helper()
	line, err := a.reader.ReadBytes('\n')
	require.NoError(t, err)
	return line
}

func (a *attachUnderTest) writeLine(t *testing.T, line string) {
	t.Helper()
	_, err := io.WriteString(a.client, line+"\n")
	require.NoError(t, err)
}

// awaitLogs returns the log output once attach has returned; reading it only
// after attach returns keeps the buffer free of concurrent writes.
func (a *attachUnderTest) awaitLogs(t *testing.T) string {
	t.Helper()
	select {
	case <-a.done:
	case <-time.After(serveTestDeadline):
		t.Fatal("attach did not return")
	}
	return a.logs.String()
}

func warnRecords(logs string) []string {
	var warns []string
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "level=WARN") {
			warns = append(warns, line)
		}
	}
	return warns
}

func TestAttach_LogsTheSessionErrorOfAnInvalidLine(t *testing.T) {
	a := startAttach(t)

	a.writeLine(t, `{"jsonrpc":"2.0","id":1,}`)

	warns := warnRecords(a.awaitLogs(t))
	require.Len(t, warns, 1)
	assert.Contains(t, warns[0], "invalid character")
	assert.Contains(t, warns[0], " session="+a.ready.Session+" ")
}

func TestAttach_CleanClientCloseIsNotLogged(t *testing.T) {
	a := startAttach(t)
	a.writeLine(t, attachTestInitialize)
	a.readLine(t)

	require.NoError(t, a.client.Close())

	assert.Empty(t, warnRecords(a.awaitLogs(t)))
}

func TestAttach_CancellationIsNotLogged(t *testing.T) {
	a := startAttach(t)
	a.writeLine(t, attachTestInitialize)
	a.readLine(t)

	a.cancel()

	assert.Empty(t, warnRecords(a.awaitLogs(t)))
}
