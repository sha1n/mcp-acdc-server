//go:build unix

package sharedserver

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const helloTestLinger = 300 * time.Millisecond

// newHelloTestAcceptor starts an acceptor with the given linger and hello
// timeout; the caller decides whether and when the build completes.
func newHelloTestAcceptor(t *testing.T, linger, helloTimeout time.Duration) (*sessionAcceptor, string, *syncLogBuffer, context.CancelFunc) {
	t.Helper()
	listener, socketPath := bindTestEndpoint(t)
	ep, err := endpoint.OpenEndpoint(filepath.Dir(socketPath))
	require.NoError(t, err)
	logs := &syncLogBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	acceptor := newSessionAcceptor(ctx, slog.New(newSessionHandler(slog.NewTextHandler(logs, nil))), listener, ep, testIdentity, linger)
	acceptor.helloTimeout = helloTimeout
	acceptor.start()
	return acceptor, socketPath, logs, cancel
}

func serveHelloTestAcceptor(acceptor *sessionAcceptor) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- acceptor.serve(mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil), nil, "")
	}()
	return done
}

func awaitServeResult(t *testing.T, done <-chan error, within time.Duration, why string) {
	t.Helper()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(within):
		t.Fatal(why)
	}
}

func TestSessionAcceptor_AConnectionWaitingForItsHelloDoesNotHoldOffTheLinger(t *testing.T) {
	acceptor, socketPath, _, _ := newHelloTestAcceptor(t, helloTestLinger, time.Hour)
	done := serveHelloTestAcceptor(acceptor)

	conn, _ := dialRawAcceptorTestSocket(t, socketPath)
	defer func() { _ = conn.Close() }()

	awaitServeResult(t, done, serveTestDeadline, "a connection without a hello must not count as an attached session")
	assert.True(t, acceptor.lingeredOut)
}

func TestSessionAcceptor_RefusedHellosDoNotRestartTheLinger(t *testing.T) {
	acceptor, socketPath, _, _ := newHelloTestAcceptor(t, helloTestLinger, serveTestDeadline)
	done := serveHelloTestAcceptor(acceptor)

	deadline := time.After(serveTestDeadline)
	for {
		select {
		case err := <-done:
			assert.NoError(t, err)
			assert.True(t, acceptor.lingeredOut)
			return
		case <-deadline:
			t.Fatal("connections refused for a bad hello must not keep the shared server alive")
		case <-time.After(helloTestLinger / 6):
		}
		if conn, err := net.Dial("unix", socketPath); err == nil {
			_, _ = io.WriteString(conn, "not a hello\n")
			_ = conn.Close()
		}
	}
}

func TestSessionAcceptor_RefusesAMissingHelloWhileTheBuildIsStillRunning(t *testing.T) {
	acceptor, socketPath, logs, cancel := newHelloTestAcceptor(t, time.Hour, 50*time.Millisecond)
	t.Cleanup(func() {
		cancel()
		_ = acceptor.abandon(context.Canceled)
	})

	conn, reader := dialRawAcceptorTestSocket(t, socketPath)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(serveTestDeadline)))

	line, err := reader.ReadBytes('\n')
	assert.ErrorIs(t, err, io.EOF, "the hello timeout runs from accept, not from the end of the build")
	assert.Empty(t, line)
	assert.Eventually(t, func() bool { return strings.Contains(logs.String(), refusedHelloLog) }, serveTestDeadline, 10*time.Millisecond)
	select {
	case <-acceptor.built:
		t.Fatal("the build must still be running")
	default:
	}
}

func TestSessionAcceptor_AShutdownInterruptsAPendingHelloAtOnce(t *testing.T) {
	listener, socketPath := bindTestEndpoint(t)
	ep, err := endpoint.OpenEndpoint(filepath.Dir(socketPath))
	require.NoError(t, err)
	peerListener, peerSocket := bindTestEndpoint(t)
	ctx, cancel := context.WithCancel(context.Background())
	acceptor := newSessionAcceptor(ctx, slog.New(slog.DiscardHandler), listener, ep, testIdentity, time.Hour)
	acceptor.helloTimeout = time.Hour
	acceptor.start()
	cancel()
	require.NoError(t, acceptor.serve(mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil), nil, ""))

	// The shutdown has already happened when each hello read starts, so the
	// hello deadline must never replace the immediate one it sets.
	for range 200 {
		client, err := net.Dial("unix", peerSocket)
		require.NoError(t, err)
		conn, err := peerListener.AcceptUnix()
		require.NoError(t, err)

		handled := make(chan struct{})
		go func() {
			acceptor.handle(conn, time.Now())
			close(handled)
		}()
		select {
		case <-handled:
		case <-time.After(2 * time.Second):
			_ = conn.Close()
			_ = client.Close()
			t.Fatal("a shutdown must end a pending hello read at once")
		}
		received, err := io.ReadAll(bufio.NewReader(client))
		_ = client.Close()
		require.NoError(t, err)
		assert.Empty(t, received)
	}
}
