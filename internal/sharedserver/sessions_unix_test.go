//go:build unix

package sharedserver

import (
	"bufio"
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dialAcceptorTestSocket(t *testing.T, socketPath string) (net.Conn, *bufio.Reader) {
	t.Helper()
	deadline := time.Now().Add(serveTestDeadline)
	for {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			require.NoError(t, conn.SetDeadline(time.Now().Add(serveTestDeadline)))
			t.Cleanup(func() { _ = conn.Close() })
			return conn, bufio.NewReader(conn)
		}
		if time.Now().After(deadline) {
			t.Fatalf("socket %s never accepted a connection: %v", socketPath, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSessionAcceptor_AWakeLosingToAnAttachKeepsAccepting(t *testing.T) {
	listener, socketPath := bindTestEndpoint(t)
	ctx, cancel := context.WithCancel(context.Background())
	acceptor := startAccepting(ctx, listener, socketPath, time.Hour)
	done := make(chan error, 1)
	go func() {
		done <- acceptor.serve(mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil), nil, "")
	}()
	_, first := dialAcceptorTestSocket(t, socketPath)
	_, err := first.ReadBytes('\n')
	require.NoError(t, err)

	acceptor.wake.wake()

	_, second := dialAcceptorTestSocket(t, socketPath)
	_, err = second.ReadBytes('\n')
	require.NoError(t, err, "a wake without an expired linger must not stop accepting")
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(serveTestDeadline):
		t.Fatal("serve did not return after cancellation")
	}
}

func TestRun_BuildFailureDropsAConnectionWaitingForTheReadyLine(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, testIdentity+".sock")
	errBuild := errors.New("build failed")
	dialed := make(chan struct{})
	build := func(context.Context) (*mcp.Server, func(), error) {
		<-dialed
		return nil, nil, errBuild
	}
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Config{Identity: testIdentity, EndpointDir: dir, Build: build, Linger: time.Hour})
	}()

	conn, _ := dialAcceptorTestSocket(t, socketPath)
	close(dialed)

	select {
	case err := <-done:
		require.ErrorIs(t, err, errBuild)
	case <-time.After(serveTestDeadline):
		t.Fatal("Run did not return after the build failed")
	}
	received, err := io.ReadAll(conn)
	if err != nil {
		assert.ErrorIs(t, err, syscall.ECONNRESET, "a connection still in the backlog is reset")
	}
	assert.Empty(t, received, "nothing may be sent before a failed build")
	_, err = os.Lstat(socketPath)
	assert.False(t, errors.Is(err, fs.ErrNotExist), "a failed build is not a linger shutdown and leaves the socket path")
	assertLockReleased(t, dir)
}
