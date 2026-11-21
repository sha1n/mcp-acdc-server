//go:build unix

package sharedserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPeerDisconnected_ClassifiesTheWaysAPeerCanGoAway(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "end of file", err: io.EOF, want: true},
		{name: "reset", err: &net.OpError{Op: "read", Net: "unix", Err: os.NewSyscallError("read", syscall.ECONNRESET)}, want: true},
		{name: "broken pipe", err: &net.OpError{Op: "write", Net: "unix", Err: os.NewSyscallError("write", syscall.EPIPE)}, want: true},
		{name: "closed connection", err: &net.OpError{Op: "read", Net: "unix", Err: net.ErrClosed}, want: true},
		{name: "wrapped reset", err: fmt.Errorf("session: %w", syscall.ECONNRESET), want: true},
		{name: "context cancellation", err: context.Canceled, want: true},
		{name: "invalid line", err: errors.New("invalid character '}' looking for beginning of object key string"), want: false},
		{name: "unexpected end of file", err: io.ErrUnexpectedEOF, want: false},
		{name: "other errno", err: os.NewSyscallError("write", syscall.EACCES), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, peerDisconnected(tt.err))
		})
	}
}

func errorOrWarnRecords(logs string) []string {
	var records []string
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "level=WARN") || strings.Contains(line, "level=ERROR") {
			records = append(records, line)
		}
	}
	return records
}

// awaitUnreadData blocks until the peer has written to conn, without
// consuming it: a close with unread received data is what makes Linux reset
// the connection.
func awaitUnreadData(t *testing.T, conn net.Conn) {
	t.Helper()
	raw, err := conn.(*net.UnixConn).SyscallConn()
	require.NoError(t, err)
	var peekErr error
	err = raw.Read(func(fd uintptr) bool {
		var probe [1]byte
		_, _, peekErr = syscall.Recvfrom(int(fd), probe[:], syscall.MSG_PEEK)
		return !errors.Is(peekErr, syscall.EAGAIN)
	})
	require.NoError(t, err)
	require.NoError(t, peekErr)
}

func TestAttach_PeerClosingWithUnreadDataIsNotLogged(t *testing.T) {
	a := startAttach(t)
	a.writeLine(t, attachTestInitialize)
	awaitUnreadData(t, a.client)

	require.NoError(t, a.client.Close())

	assert.Empty(t, errorOrWarnRecords(a.awaitLogs(t)))
}

// peerBeforeReadyAttempt is one shared server whose build waits for the
// test. Its initial linger starts at bind, so an attempt whose dial or accept
// misses the first second is lost and repeated with a fresh identity
// directory (#196).
type peerBeforeReadyAttempt struct {
	ep         endpoint.Endpoint
	dir        string
	logDir     string
	startedAt  time.Time
	release    chan struct{}
	releasedAt time.Time
	cancel     context.CancelFunc
	done       chan peerBeforeReadyExit
	exit       *peerBeforeReadyExit
}

type peerBeforeReadyExit struct {
	err error
	at  time.Time
}

func startPeerBeforeReadyAttempt(t *testing.T) *peerBeforeReadyAttempt {
	t.Helper()
	root := shortTempDir(t)
	a := &peerBeforeReadyAttempt{
		dir:     filepath.Join(root, "ep"),
		logDir:  filepath.Join(root, "logs"),
		release: make(chan struct{}),
		done:    make(chan peerBeforeReadyExit, 1),
	}
	require.NoError(t, os.Mkdir(a.logDir, 0o700))
	ep, err := endpoint.OpenEndpoint(a.dir)
	require.NoError(t, err)
	a.ep = ep
	build := func(ctx context.Context, _ *slog.Logger) (*mcp.Server, func(), error) {
		select {
		case <-a.release:
			return mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil), nil, nil
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.startedAt = time.Now()
	go func() {
		err := Run(ctx, Config{
			Identity:        testIdentity,
			CurrentIdentity: currentIsTestIdentity,
			EndpointDir:     a.dir,
			LogDir:          a.logDir,
			Build:           build,
			Linger:          config.MinSharedServerLinger,
		})
		a.done <- peerBeforeReadyExit{err: err, at: time.Now()}
	}()
	t.Cleanup(func() {
		cancel()
		a.releaseBuild()
		if _, settled := a.awaitExitBy(time.Now().Add(serveTestDeadline)); !settled {
			t.Error("Run did not return after cancellation")
		}
	})
	return a
}

func (a *peerBeforeReadyAttempt) releaseBuild() {
	select {
	case <-a.release:
	default:
		a.releasedAt = time.Now()
		close(a.release)
	}
}

func (a *peerBeforeReadyAttempt) awaitExitBy(deadline time.Time) (peerBeforeReadyExit, bool) {
	if a.exit != nil {
		return *a.exit, true
	}
	select {
	case exit := <-a.done:
		a.exit = &exit
		return exit, true
	case <-time.After(time.Until(deadline)):
		return peerBeforeReadyExit{}, false
	}
}

func (a *peerBeforeReadyAttempt) requireExitBy(t *testing.T, deadline time.Time) peerBeforeReadyExit {
	t.Helper()
	exit, settled := a.awaitExitBy(deadline)
	require.True(t, settled, "Run did not return")
	return exit
}

// requireCleanLingerExit accepts a lost attempt only when it ended the way an
// expired initial linger ends a shared server.
func (a *peerBeforeReadyAttempt) requireCleanLingerExit(t *testing.T, deadline time.Time) {
	t.Helper()
	exit := a.requireExitBy(t, deadline)
	require.NoError(t, exit.err, "a lost attempt must be a normal linger exit")
	require.GreaterOrEqual(t, exit.at.Sub(a.startedAt), config.MinSharedServerLinger, "a lost attempt exited before the linger elapsed")
	socketPath, err := a.ep.SocketPath(testIdentity)
	require.NoError(t, err)
	_, err = os.Lstat(socketPath)
	require.ErrorIs(t, err, fs.ErrNotExist, "a lost attempt must remove its socket")
	assertLockReleased(t, a.dir)
}

// dialSession dials identity's socket in ep until it accepts, and reports
// false once Run has ended without accepting.
func dialSession(t *testing.T, ep endpoint.Endpoint, identity string, done chan peerBeforeReadyExit, deadline time.Time) (net.Conn, bool) {
	t.Helper()
	socketPath, err := ep.SocketPath(identity)
	require.NoError(t, err)
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		conn, err := (&net.Dialer{Deadline: deadline}).Dial("unix", socketPath)
		if err == nil {
			sendTestHello(t, conn)
			return conn, true
		}
		require.True(t, errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED),
			"dial failed other than by a missing or closed socket: %v", err)
		select {
		case exit := <-done:
			done <- exit
			return nil, false
		case <-poll.C:
		case <-time.After(time.Until(deadline)):
			t.Fatalf("socket never accepted: %v", err)
		}
	}
}

// closeBeforeTheReadyLine reports whether the session attached within the
// initial linger: only then does its own linger, started when it ends after
// the release, hold Run for a full linger past the release.
func (a *peerBeforeReadyAttempt) closeBeforeTheReadyLine(t *testing.T, deadline time.Time) bool {
	t.Helper()
	conn, dialed := dialSession(t, a.ep, testIdentity, a.done, deadline)
	if !dialed {
		a.requireCleanLingerExit(t, deadline)
		return false
	}
	require.NoError(t, conn.Close())
	a.releaseBuild()
	exit := a.requireExitBy(t, deadline)
	if exit.at.Sub(a.releasedAt) < config.MinSharedServerLinger {
		a.requireCleanLingerExit(t, deadline)
		return false
	}
	require.NoError(t, exit.err)
	return true
}

func TestRun_PeerClosingBeforeTheReadyLineIsNotLogged(t *testing.T) {
	deadline := time.Now().Add(3 * serveTestDeadline)
	for attempt := 1; ; attempt++ {
		require.True(t, time.Now().Before(deadline), "no attempt attached within its initial linger (%d attempts)", attempt-1)
		a := startPeerBeforeReadyAttempt(t)
		if !a.closeBeforeTheReadyLine(t, deadline) {
			continue
		}
		logs, err := os.ReadFile(filepath.Join(a.logDir, testIdentity+".log"))
		require.NoError(t, err)
		assert.Empty(t, errorOrWarnRecords(string(logs)))
		return
	}
}
