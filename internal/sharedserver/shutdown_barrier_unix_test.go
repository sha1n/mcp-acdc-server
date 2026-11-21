//go:build unix

package sharedserver

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const barrierTestPing = `{"jsonrpc":"2.0","id":2,"method":"ping"}`

type barrierTestAcceptor struct {
	acceptor   *sessionAcceptor
	listener   *net.UnixListener
	socketPath string
	cancel     context.CancelFunc
	deadline   time.Time
}

// startBarrierTestAcceptor registers the bounded join of the stop sequence
// first, so that every release registered later runs before it.
func startBarrierTestAcceptor(t *testing.T, finish func(*sessionAcceptor)) *barrierTestAcceptor {
	t.Helper()
	deadline := time.Now().Add(serveTestDeadline)
	listener, socketPath := bindTestEndpoint(t)
	ep, err := endpoint.OpenEndpoint(filepath.Dir(socketPath))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	acceptor := startAccepting(ctx, slog.New(slog.DiscardHandler), listener, ep, testIdentity, time.Hour)
	t.Cleanup(func() {
		cancel()
		joined := make(chan struct{})
		go func() {
			finish(acceptor)
			close(joined)
		}()
		select {
		case <-joined:
		case <-time.After(time.Until(deadline)):
			t.Error("the shared server did not stop")
		}
	})
	return &barrierTestAcceptor{acceptor: acceptor, listener: listener, socketPath: socketPath, cancel: cancel, deadline: deadline}
}

// holdListenerDescriptor keeps a reference to the listener's descriptor, so
// that a Close of the listener cannot complete until the release.
func (b *barrierTestAcceptor) holdListenerDescriptor(t *testing.T) func() {
	t.Helper()
	raw, err := b.listener.SyscallConn()
	require.NoError(t, err)
	held := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- raw.Control(func(uintptr) {
			close(held)
			<-release
		})
	}()
	var once sync.Once
	releaseOnce := func() {
		once.Do(func() {
			close(release)
			if err := <-finished; err != nil {
				t.Errorf("the listener descriptor hold failed: %v", err)
			}
		})
	}
	t.Cleanup(releaseOnce)
	select {
	case <-held:
	case err := <-finished:
		finished <- err
		t.Fatalf("the listener descriptor could not be held: %v", err)
	case <-time.After(time.Until(b.deadline)):
		t.Fatal("the listener descriptor was never held")
	}
	return releaseOnce
}

func (b *barrierTestAcceptor) dial(t *testing.T) *net.UnixConn {
	t.Helper()
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: b.socketPath, Net: "unix"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(b.deadline))
	sendTestHello(t, conn)
	return conn
}

func (b *barrierTestAcceptor) requireDialRefused(t *testing.T) {
	t.Helper()
	conn, err := net.Dial("unix", b.socketPath)
	if err == nil {
		_ = conn.Close()
	}
	require.ErrorIs(t, err, syscall.ECONNREFUSED, "once accepting has stopped, a dial must be refused")
}

func (b *barrierTestAcceptor) awaitAcceptedConnections(t *testing.T, want int) {
	t.Helper()
	for {
		b.acceptor.linger.mu.Lock()
		accepted := b.acceptor.linger.sessions
		b.acceptor.linger.mu.Unlock()
		if accepted == want {
			return
		}
		if time.Now().After(b.deadline) {
			t.Fatalf("accepted connections: %d, want %d", accepted, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// requireDroppedWithoutData accepts EOF or a reset: Linux resets a connection
// that is closed while it is still in the backlog.
func requireDroppedWithoutData(t *testing.T, reader io.Reader, msgAndArgs ...any) {
	t.Helper()
	received, err := io.ReadAll(reader)
	if err != nil {
		require.ErrorIs(t, err, syscall.ECONNRESET, msgAndArgs...)
	}
	assert.Empty(t, string(received), msgAndArgs...)
}

// peekWithoutWaiting reports the state of conn at this instant: EAGAIN while
// it is open with nothing to read, io.EOF or a reset once the server dropped it.
func peekWithoutWaiting(t *testing.T, conn *net.UnixConn) error {
	t.Helper()
	raw, err := conn.SyscallConn()
	require.NoError(t, err)
	var peekErr error
	require.NoError(t, raw.Read(func(fd uintptr) bool {
		n, _, err := syscall.Recvfrom(int(fd), make([]byte, 1), syscall.MSG_PEEK|syscall.MSG_DONTWAIT)
		switch {
		case err != nil:
			peekErr = err
		case n == 0:
			peekErr = io.EOF
		}
		return true
	}))
	return peekErr
}

func TestSessionAcceptor_AnAttachedSessionOutlivesAListenerCloseThatHasNotCompleted(t *testing.T) {
	admitted := make(chan context.Context, 1)
	served := make(chan error, 1)
	b := startBarrierTestAcceptor(t, func(*sessionAcceptor) { assert.NoError(t, <-served) })
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	go func() {
		served <- b.acceptor.serve(server, func(ctx context.Context) { admitted <- ctx }, "")
	}()

	session := b.dial(t)
	reader := bufio.NewReader(session)
	readyLine, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Contains(t, readyLine, `"ready":true`)
	sessionCtx := <-admitted
	writeAttachTestLine(t, session, attachTestInitialize)
	readAttachTestLine(t, reader)
	writeAttachTestLine(t, session, attachTestInitialized)

	release := b.holdListenerDescriptor(t)
	b.cancel()
	require.NoError(t, sessionCtx.Err(), "a session must not end before accepting has stopped")
	writeAttachTestLine(t, session, barrierTestPing)
	require.Contains(t, string(readAttachTestLine(t, reader)), `"id":2`, "the session must still be served")
	require.NoError(t, sessionCtx.Err(), "a session must not end while the listener close has not completed")

	release()
	select {
	case <-sessionCtx.Done():
	case <-time.After(time.Until(b.deadline)):
		t.Fatal("the session never ended")
	}
	_, err = io.Copy(io.Discard, reader)
	require.NoError(t, err, "the session must end by EOF")
	b.requireDialRefused(t)
}

// Detection of an early drop is scheduling-dependent: no observable event
// follows the cancelled handler's decision, so the peek can precede a wrong
// close. It never fails on correct code; the attached-session test is the
// deterministic guard of the ordering. Without a production seam no
// deterministic construction exists.
func TestSessionAcceptor_AConnectionWaitingForTheBuildIsDroppedOnlyOnceAcceptingHasStopped(t *testing.T) {
	tests := []struct {
		name string
		stop func(b *barrierTestAcceptor, abandoned chan<- error)
	}{
		{
			name: "run cancelled",
			stop: func(b *barrierTestAcceptor, _ chan<- error) { b.cancel() },
		},
		{
			name: "build failed",
			stop: func(b *barrierTestAcceptor, abandoned chan<- error) {
				go func() { abandoned <- b.acceptor.abandon(errors.New("build failed")) }()
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			abandoned := make(chan error, 1)
			b := startBarrierTestAcceptor(t, func(a *sessionAcceptor) { <-a.stopped })
			waiting := b.dial(t)
			b.awaitAcceptedConnections(t, 1)

			release := b.holdListenerDescriptor(t)
			tt.stop(b, abandoned)
			probe := b.dial(t)
			assert.ErrorIs(t, peekWithoutWaiting(t, waiting), syscall.EAGAIN,
				"a connection must not be dropped before accepting has stopped")

			release()
			requireDroppedWithoutData(t, waiting, "the waiting connection must be dropped without a ready line")
			requireDroppedWithoutData(t, probe, "a connection made while closing must be dropped without a ready line")
			b.requireDialRefused(t)
		})
	}
}

// Detection of a missing or misplaced admission read is scheduling-dependent:
// session cancellation can close the transport before a wrong ready write, so
// the empty read also matches wrong code. It never fails on correct code; the
// attached-session test is the deterministic guard of the ordering. Without a
// production seam no deterministic construction exists.
func TestSessionAcceptor_AConnectionRevalidatedWhileShutdownBeginsGetsNoReadyLine(t *testing.T) {
	served := make(chan error, 1)
	b := startBarrierTestAcceptor(t, func(*sessionAcceptor) { assert.NoError(t, <-served) })
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	go func() {
		served <- b.acceptor.serve(server, func(context.Context) { b.cancel() }, "")
	}()

	conn := b.dial(t)
	requireDroppedWithoutData(t, conn, "a shared server that is shutting down must not send a ready line")
	b.requireDialRefused(t)
}
