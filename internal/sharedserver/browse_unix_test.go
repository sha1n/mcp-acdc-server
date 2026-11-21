//go:build unix

package sharedserver

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// browse dials the acceptor, sends a browse hello and returns the reply line
// and whatever the server sent after it before closing.
func browse(t *testing.T, socketPath string, maxResults int) (line []byte, rest []byte) {
	t.Helper()
	conn, reader := dialRawAcceptorTestSocket(t, socketPath)
	defer func() { _ = conn.Close() }()
	hello, err := EncodeBrowseHelloLine(maxResults)
	require.NoError(t, err)
	_, err = conn.Write(hello)
	require.NoError(t, err)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(serveTestDeadline)))
	line, err = reader.ReadBytes('\n')
	require.NoError(t, err)
	rest, err = io.ReadAll(reader)
	require.NoError(t, err, "the server closes a browse connection after its reply")
	return line, rest
}

func newBrowseTestAcceptor(t *testing.T, linger time.Duration) (*sessionAcceptor, string, *syncLogBuffer, *recordingUIHandler) {
	t.Helper()
	acceptor, socketPath, logs, cancel := newHelloTestAcceptor(t, linger, serveTestDeadline)
	handler := &recordingUIHandler{}
	acceptor.setUI(handler.build, nil)
	t.Cleanup(func() {
		cancel()
		<-acceptor.stopped
	})
	return acceptor, socketPath, logs, handler
}

func TestSessionAcceptor_BrowseRepliesWithTheUIURLAndCloses(t *testing.T) {
	acceptor, socketPath, logs, _ := newBrowseTestAcceptor(t, time.Hour)
	serveHelloTestAcceptor(acceptor)

	line, rest := browse(t, socketPath, 7)

	ready, err := DecodeBrowseReadyLine(line)
	require.NoError(t, err)
	assert.Empty(t, ready.Session, "a browse connection is not an attached session")
	assert.Empty(t, rest)
	assert.Equal(t, "/ui/ 7", getBody(t, ready.UIURL))
	assert.NotContains(t, logs.String(), "Session attached")
	assert.Contains(t, logs.String(), "Web UI started")
}

func TestSessionAcceptor_BrowseWaitsForTheBuild(t *testing.T) {
	acceptor, socketPath, _, _ := newBrowseTestAcceptor(t, time.Hour)
	conn, reader := dialRawAcceptorTestSocket(t, socketPath)
	hello, err := EncodeBrowseHelloLine(7)
	require.NoError(t, err)
	_, err = conn.Write(hello)
	require.NoError(t, err)
	replied := make(chan []byte, 1)
	go func() {
		line, _ := reader.ReadBytes('\n')
		replied <- line
	}()

	select {
	case <-replied:
		t.Fatal("a browse request must wait for the build")
	case <-time.After(100 * time.Millisecond):
	}
	serveHelloTestAcceptor(acceptor)

	select {
	case line := <-replied:
		_, err := DecodeBrowseReadyLine(line)
		assert.NoError(t, err)
	case <-time.After(serveTestDeadline):
		t.Fatal("a browse request must be answered once the build completes")
	}
}

func TestSessionAcceptor_EveryBrowseGetsTheSameURLAndTheFirstMaxResults(t *testing.T) {
	acceptor, socketPath, _, handler := newBrowseTestAcceptor(t, time.Hour)
	serveHelloTestAcceptor(acceptor)

	first, _ := browse(t, socketPath, 7)
	second, _ := browse(t, socketPath, 99)

	assert.Equal(t, string(first), string(second))
	assert.Equal(t, []int{7}, handler.builtWith())
}

func TestSessionAcceptor_BrowseBindFailureRepliesWithAnErrorAndTheNextBrowseRetries(t *testing.T) {
	acceptor, socketPath, _, _ := newBrowseTestAcceptor(t, time.Hour)
	failFirstListen(acceptor.ui)
	serveHelloTestAcceptor(acceptor)

	line, rest := browse(t, socketPath, 7)
	_, err := DecodeBrowseReadyLine(line)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "address in use")
	assert.Empty(t, rest)

	line, _ = browse(t, socketPath, 7)
	ready, err := DecodeBrowseReadyLine(line)
	require.NoError(t, err)
	assert.Equal(t, "/ui/ 7", getBody(t, ready.UIURL))
}

func TestSessionAcceptor_BrowseBindFailureLeavesAttachedSessionsServing(t *testing.T) {
	acceptor, socketPath, _, _ := newBrowseTestAcceptor(t, time.Hour)
	acceptor.ui.listen = func() (net.Listener, error) { return nil, errors.New("address in use") }
	serveHelloTestAcceptor(acceptor)
	session, sessionReader := dialAcceptorTestSocket(t, socketPath)
	defer func() { _ = session.Close() }()
	_, err := DecodeReadyLine(readAttachTestLine(t, sessionReader))
	require.NoError(t, err)

	_, _ = browse(t, socketPath, 7)

	assertSessionResponds(t, session, sessionReader)
}

func TestSessionAcceptor_BrowseDoesNotHoldOffTheLinger(t *testing.T) {
	acceptor, socketPath, _, _ := newBrowseTestAcceptor(t, helloTestLinger)
	done := serveHelloTestAcceptor(acceptor)

	_, _ = browse(t, socketPath, 7)

	awaitServeResult(t, done, serveTestDeadline, "a browse connection must not count as an attached session")
	assert.True(t, acceptor.lingeredOut)
}

func TestSessionAcceptor_StopClosesTheUI(t *testing.T) {
	acceptor, socketPath, _, _ := newBrowseTestAcceptor(t, helloTestLinger)
	done := serveHelloTestAcceptor(acceptor)
	line, _ := browse(t, socketPath, 7)
	ready, err := DecodeBrowseReadyLine(line)
	require.NoError(t, err)

	awaitServeResult(t, done, serveTestDeadline, "the shared server must linger out")

	_, err = http.Get(ready.UIURL) //nolint:gosec // test URL from the UI under test
	assert.Error(t, err, "the UI lives only as long as its shared server")
}

// assertSessionResponds checks that an attached session still answers an
// initialize request.
func assertSessionResponds(t *testing.T, conn net.Conn, reader *bufio.Reader) {
	t.Helper()
	require.NoError(t, conn.SetDeadline(time.Now().Add(serveTestDeadline)))
	_, err := io.WriteString(conn, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`+"\n")
	require.NoError(t, err)
	line, err := reader.ReadBytes('\n')
	require.NoError(t, err)
	assert.Contains(t, string(line), `"id":1`)
}

func TestSessionAcceptor_APendingBrowseOutlivesABuildLongerThanTheLinger(t *testing.T) {
	acceptor, socketPath, _, _ := newBrowseTestAcceptor(t, helloTestLinger)
	conn, reader := dialRawAcceptorTestSocket(t, socketPath)
	hello, err := EncodeBrowseHelloLine(7)
	require.NoError(t, err)
	_, err = conn.Write(hello)
	require.NoError(t, err)

	time.Sleep(3 * helloTestLinger)
	done := serveHelloTestAcceptor(acceptor)

	line, err := reader.ReadBytes('\n')
	require.NoError(t, err)
	_, err = DecodeBrowseReadyLine(line)
	require.NoError(t, err, "a browse waits for the build without a time limit")
	awaitServeResult(t, done, serveTestDeadline, "once answered, a browse must not keep the shared server alive")
}

func TestSessionAcceptor_RepeatedBrowsesDoNotPostponeTheLinger(t *testing.T) {
	acceptor, socketPath, _, _ := newBrowseTestAcceptor(t, helloTestLinger)
	done := serveHelloTestAcceptor(acceptor)
	stop := make(chan struct{})
	polled := make(chan struct{})
	go func() {
		defer close(polled)
		for {
			select {
			case <-stop:
				return
			case <-time.After(helloTestLinger / 6):
				browseIgnoringErrors(socketPath)
			}
		}
	}()
	defer func() {
		close(stop)
		<-polled
	}()

	awaitServeResult(t, done, 3*helloTestLinger, "browse requests must not reset or extend the linger")
	assert.True(t, acceptor.lingeredOut)
}

// browseIgnoringErrors sends one browse request and reads its reply, if any;
// it may race the shutdown, so every error is ignored.
func browseIgnoringErrors(socketPath string) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	hello, err := EncodeBrowseHelloLine(7)
	if err != nil {
		return
	}
	if _, err := conn.Write(hello); err != nil {
		return
	}
	_, _ = io.ReadAll(conn)
}
