//go:build unix

package sharedserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
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

func TestRun_PeerClosingBeforeTheReadyLineIsNotLogged(t *testing.T) {
	root := shortTempDir(t)
	endpointDir := filepath.Join(root, "ep")
	logDir := filepath.Join(root, "logs")
	require.NoError(t, os.Mkdir(logDir, 0o700))
	release := make(chan struct{})
	build := func(context.Context, *slog.Logger) (*mcp.Server, func(), error) {
		<-release
		return mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil), nil, nil
	}
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Config{
			Identity:    testIdentity,
			EndpointDir: endpointDir,
			LogDir:      logDir,
			Build:       build,
			Linger:      config.MinSharedServerLinger,
		})
	}()

	conn := dialAttachRevalidateSocket(t, filepath.Join(endpointDir, testIdentity+".sock"), done)
	require.NoError(t, conn.Close())
	close(release)

	require.NoError(t, awaitRunResult(t, done))
	logs, err := os.ReadFile(filepath.Join(logDir, testIdentity+".log"))
	require.NoError(t, err)
	assert.Empty(t, errorOrWarnRecords(string(logs)))
}
