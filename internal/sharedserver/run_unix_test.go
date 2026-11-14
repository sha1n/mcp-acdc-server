//go:build unix

package sharedserver

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shortTempDir lives under /tmp because the per-test directory of t.TempDir
// can push a socket path past the darwin sun_path limit.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "acdc-run-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func lockFileIn(dir string) string {
	return filepath.Join(dir, testIdentity+".lock")
}

func bindTestEndpoint(t *testing.T) (*net.UnixListener, string) {
	t.Helper()
	ep, err := endpoint.OpenEndpoint(filepath.Join(shortTempDir(t), "ep"))
	require.NoError(t, err)
	socketPath, err := ep.SocketPath(testIdentity)
	require.NoError(t, err)
	listener, err := bind(ep, testIdentity)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	return listener, socketPath
}

func buildSpy(err error) (BuildFunc, *bool) {
	built := false
	return func(context.Context) (*mcp.Server, func(), error) {
		built = true
		if err != nil {
			return nil, nil, err
		}
		return mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil), nil, nil
	}, &built
}

func assertLockReleased(t *testing.T, dir string) {
	t.Helper()
	lock := flock.New(lockFileIn(dir))
	locked, err := lock.TryLock()
	require.NoError(t, err)
	assert.True(t, locked, "the lock must be released when Run returns")
	_ = lock.Unlock()
}

func TestRun_RejectsAnInvalidIdentity(t *testing.T) {
	dir := filepath.Join(shortTempDir(t), "ep")
	build, built := buildSpy(nil)

	err := Run(context.Background(), Config{Identity: "../escape", EndpointDir: dir, Build: build})

	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid shared server identity "../escape"`)
	assert.NoDirExists(t, dir, "nothing may be created for an invalid identity")
	assert.False(t, *built)
}

func TestRun_EndpointDirCheckFailureStopsBeforeTheLock(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, dir string)
		wantErr string
	}{
		{
			name:    "not a directory",
			prepare: func(t *testing.T, dir string) { require.NoError(t, os.WriteFile(dir, nil, 0o600)) },
			wantErr: "failed to create endpoint directory",
		},
		{
			name: "mode 0755",
			prepare: func(t *testing.T, dir string) {
				require.NoError(t, os.Mkdir(dir, 0o700))
				require.NoError(t, os.Chmod(dir, 0o755))
			},
			wantErr: "endpoint directory {dir}: mode 0755",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(shortTempDir(t), "ep")
			tt.prepare(t, dir)
			build, built := buildSpy(nil)

			err := Run(context.Background(), Config{Identity: testIdentity, EndpointDir: dir, Build: build})

			require.Error(t, err)
			assert.Contains(t, err.Error(), strings.ReplaceAll(tt.wantErr, "{dir}", dir))
			assert.NoFileExists(t, lockFileIn(dir))
			assert.False(t, *built)
		})
	}
}

func TestRun_RejectsAnOverLongSocketPathBeforeTheLock(t *testing.T) {
	dir := filepath.Join(shortTempDir(t), strings.Repeat("d", 120))
	build, built := buildSpy(nil)

	err := Run(context.Background(), Config{Identity: testIdentity, EndpointDir: dir, Build: build})

	require.ErrorIs(t, err, endpoint.ErrSocketPathTooLong)
	assert.NoFileExists(t, lockFileIn(dir), "the lock must not be taken for an unusable socket path")
	assert.False(t, *built)
}

func TestRun_LockIOErrorIsReportedAsLockNotAcquired(t *testing.T) {
	dir := shortTempDir(t)
	require.NoError(t, os.Symlink(filepath.Join(dir, "missing", "lock"), lockFileIn(dir)))
	build, built := buildSpy(nil)

	err := Run(context.Background(), Config{Identity: testIdentity, EndpointDir: dir, Build: build})

	require.ErrorIs(t, err, ErrLockNotAcquired)
	assert.ErrorIs(t, err, fs.ErrNotExist, "the lock I/O error must be preserved")
	assert.NoFileExists(t, filepath.Join(dir, testIdentity+".sock"))
	assert.False(t, *built)
}

func TestRun_StaleSocketRemovalFailureReleasesTheLock(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, testIdentity+".sock")
	require.NoError(t, os.Mkdir(socketPath, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(socketPath, "keep"), nil, 0o600))
	build, built := buildSpy(nil)

	err := Run(context.Background(), Config{Identity: testIdentity, EndpointDir: dir, Build: build})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to remove stale socket")
	assert.DirExists(t, socketPath, "the unremovable entry must be left in place")
	assert.False(t, *built)
	assertLockReleased(t, dir)
}

func TestRun_BuildFailureReleasesTheLockAndClosesTheSocket(t *testing.T) {
	dir := shortTempDir(t)
	errBuild := errors.New("build failed")
	build, built := buildSpy(errBuild)

	err := Run(context.Background(), Config{Identity: testIdentity, EndpointDir: dir, Build: build})

	require.ErrorIs(t, err, errBuild)
	assert.True(t, *built)
	assertLockReleased(t, dir)
	probe, err := net.DialTimeout("unix", filepath.Join(dir, testIdentity+".sock"), time.Second)
	if err == nil {
		_ = probe.Close()
	}
	assert.Error(t, err, "the listener must be closed")
}

func TestAttach_ReadyLineWriteFailureClosesTheConnectionWithoutServing(t *testing.T) {
	listener, _ := bindTestEndpoint(t)

	client, err := net.Dial("unix", listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.SetDeadline(time.Now().Add(serveTestDeadline)))
	conn, err := listener.AcceptUnix()
	require.NoError(t, err)
	// A write after shutting down the write side fails with EPIPE every time,
	// unlike a write racing a peer close.
	require.NoError(t, conn.CloseWrite())

	done := make(chan struct{})
	go func() {
		attach(context.Background(), conn, mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil), nil)
		close(done)
	}()

	// A served session would block in Wait until the client closes, so a
	// return while the client is still open shows that serving never started.
	select {
	case <-done:
	case <-time.After(serveTestDeadline):
		t.Fatal("attach did not return after the ready line write failed")
	}
	received, err := io.ReadAll(client)
	require.NoError(t, err)
	assert.Empty(t, received, "nothing may be sent after the ready line write fails")
	_, err = conn.Read(make([]byte, 1))
	assert.ErrorIs(t, err, net.ErrClosed, "attach must close the connection")
}
