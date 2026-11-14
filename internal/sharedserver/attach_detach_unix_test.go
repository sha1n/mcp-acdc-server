//go:build unix

package sharedserver

import (
	"bufio"
	"context"
	"io"
	"net"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	attachTestInitialized = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	attachTestPing        = `{"jsonrpc":"2.0","id":2,"method":"ping"}`
)

func (a *attributedAttach) startCancellable(t *testing.T) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		defer close(a.done)
		attach(ctx, a.logger, a.serverConn, a.server, nil, "/var/log/acdc.log")
	}()
	return cancel
}

func writeAttachTestLine(t *testing.T, conn net.Conn, line string) {
	t.Helper()
	_, err := io.WriteString(conn, line+"\n")
	require.NoError(t, err)
}

func readAttachTestLine(t *testing.T, reader *bufio.Reader) []byte {
	t.Helper()
	line, err := reader.ReadBytes('\n')
	require.NoError(t, err)
	return line
}

// initializeSession completes the MCP handshake and returns the ready-line
// session id. A ping answered after notifications/initialized proves the
// server has consumed the notification, so the session is fully initialized.
func initializeSession(t *testing.T, conn net.Conn) string {
	t.Helper()
	reader := bufio.NewReader(conn)
	ready, err := DecodeReadyLine(readAttachTestLine(t, reader))
	require.NoError(t, err)
	writeAttachTestLine(t, conn, attachTestInitialize)
	readAttachTestLine(t, reader)
	writeAttachTestLine(t, conn, attachTestInitialized)
	writeAttachTestLine(t, conn, attachTestPing)
	require.Contains(t, string(readAttachTestLine(t, reader)), `"id":2`)
	return ready.Session
}

func assertOneAttachAndOneDetach(t *testing.T, logs, session string) {
	t.Helper()
	quoted := regexp.QuoteMeta(session)
	assert.Len(t, linesMatching(logs, `^time=\S+ level=INFO msg="Session attached" session=`+quoted+`$`), 1, logs)
	assert.Len(t, linesMatching(logs, `^time=\S+ level=INFO msg="Session detached" session=`+quoted+`$`), 1, logs)
	assert.Len(t, linesMatching(logs, `msg="Session (attached|detached)"`), 2, logs)
}

func TestAttach_CancellationOfAnInitializedSessionLogsOneAttachAndOneDetach(t *testing.T) {
	a := newAttributedAttach(t)
	cancel := a.startCancellable(t)
	session := initializeSession(t, a.client)

	cancel()
	logs := a.awaitLogs(t)

	assertOneAttachAndOneDetach(t, logs, session)
}

// Linux resets a connection closed with unread received data, while macOS
// reports end of file; either way the session must detach exactly once.
func TestAttach_PeerResetOfAnInitializedSessionLogsOneAttachAndOneDetach(t *testing.T) {
	a := newAttributedAttach(t)
	a.start(t)
	session := initializeSession(t, a.client)
	writeAttachTestLine(t, a.client, `{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	awaitUnreadData(t, a.client)

	require.NoError(t, a.client.Close())
	logs := a.awaitLogs(t)

	assertOneAttachAndOneDetach(t, logs, session)
}

func TestRun_ShutdownOfAnInitializedSessionLogsOneAttachAndOneDetach(t *testing.T) {
	endpointDir := shortTempDir(t)
	logDir := t.TempDir()
	build, observed := observingBuild("build marker", nil)
	run := startExplicitLoggerRun(t, Config{
		Identity:    testIdentity,
		EndpointDir: endpointDir,
		LogDir:      logDir,
		Build:       build,
		Linger:      time.Hour,
	})
	awaitExplicitLoggerBuild(t, observed)
	ep, err := endpoint.OpenEndpoint(endpointDir)
	require.NoError(t, err)
	socketPath, err := ep.SocketPath(testIdentity)
	require.NoError(t, err)
	conn, err := net.Dial("unix", socketPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(serveTestDeadline)))
	session := initializeSession(t, conn)

	run.stop(t)
	logs := readExplicitLoggerLog(t, filepath.Join(logDir, testIdentity+".log"))

	assertOneAttachAndOneDetach(t, logs, session)
}
