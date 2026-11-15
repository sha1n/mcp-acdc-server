//go:build unix

package sessionproxy_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_RelaysUnusualLineBytesVerbatim(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{name: "CRLF line ending", line: "{\"id\":1,\"method\":\"ping\"}\r\n"},
		{name: "invalid UTF-8", line: "{\"id\":\"\xff\xfe\xc0\"}\n"},
		{name: "line larger than 64 KiB", line: "{\"data\":\"" + strings.Repeat("x", 100<<10) + "\"}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := proxyTestEndpointDir(t)
			server := newFakeSharedServer(t, dir)
			host := startProxy(t, sessionproxy.Config{EndpointDir: fixedEndpointDir(dir), Spawn: server.spawn})
			conn := server.nextConn()
			conn.sendReady()

			host.write(tt.line)
			assert.Equal(t, tt.line, conn.readLine(), "host to shared server")

			written := make(chan error, 1)
			go func() {
				_, err := io.WriteString(conn.conn, tt.line)
				written <- err
			}()
			assert.Equal(t, tt.line, host.readLine(), "shared server to host")
			require.NoError(t, <-written)
		})
	}
}

func readyLine(t *testing.T) string {
	t.Helper()
	line, err := sharedserver.EncodeReadyLine("abcd1234", "/var/log/acdc.log")
	require.NoError(t, err)
	return string(line)
}

func TestRun_ConsumesAReadyLineSplitAcrossWrites(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	host := startProxy(t, sessionproxy.Config{EndpointDir: fixedEndpointDir(dir), Spawn: server.spawn})
	conn := server.nextConn()
	ready := readyLine(t)
	const response = "{\"id\":1,\"result\":{}}\n"

	conn.write(ready[:len(ready)/2])
	conn.write(ready[len(ready)/2:])
	conn.write(response)

	assert.Equal(t, response, host.readLine())
}

func TestRun_RelaysAResponseDeliveredTogetherWithTheReadyLine(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	host := startProxy(t, sessionproxy.Config{EndpointDir: fixedEndpointDir(dir), Spawn: server.spawn})
	conn := server.nextConn()
	const response = "{\"id\":1,\"result\":{}}\n"

	conn.write(readyLine(t) + response)

	assert.Equal(t, response, host.readLine())
}

func TestRun_DropsIncompleteSharedServerOutputAtEOF(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	stdin, hostWriter := io.Pipe()
	t.Cleanup(func() { _ = hostWriter.Close() })
	var stdout bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- sessionproxy.Run(context.Background(), sessionproxy.Config{
			EndpointDir: fixedEndpointDir(dir),
			Identity:    func() (string, error) { return proxyTestIdentity, nil },
			ConnectWait: proxyTestDeadline,
			Spawn:       server.spawn,
			Logger:      slog.New(slog.DiscardHandler),
			Fallback:    unexpectedFallback(t),
			Stdin:       stdin,
			Stdout:      &stdout,
		})
	}()
	conn := server.nextConn()
	const complete = "{\"id\":1,\"result\":{}}\n"

	conn.write(readyLine(t) + complete + "{\"id\":2,\"res")
	require.NoError(t, conn.conn.Close())

	server.nextConn()
	require.NoError(t, hostWriter.Close())
	require.NoError(t, awaitRunResult(t, done))
	assert.Equal(t, complete, stdout.String())
}

func endpointEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestRun_AttachesWithOnlyTheSocketPresentAndCreatesNoOtherFile(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	require.NoError(t, server.spawn(proxyTestIdentity))
	require.Equal(t, []string{filepath.Base(server.socketPath)}, endpointEntries(t, dir))

	host := startProxy(t, sessionproxy.Config{EndpointDir: fixedEndpointDir(dir), Spawn: server.spawn})
	conn := server.nextConn()
	conn.sendReady()
	host.write("{\"id\":1}\n")
	require.Equal(t, "{\"id\":1}\n", conn.readLine(), "the session is relaying")

	assert.Equal(t, []string{filepath.Base(server.socketPath)}, endpointEntries(t, dir))
	assert.EqualValues(t, 1, server.spawns.Load(), "the proxy attached without spawning")
}

func TestRun_AttachesAlongsideAnUnreadableLockFile(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	ep, err := endpoint.OpenEndpoint(dir)
	require.NoError(t, err)
	lockPath, err := ep.LockPath(proxyTestIdentity)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(lockPath, nil, 0o000))
	require.NoError(t, server.spawn(proxyTestIdentity))

	host := startProxy(t, sessionproxy.Config{EndpointDir: fixedEndpointDir(dir), Spawn: server.spawn})
	conn := server.nextConn()
	conn.sendReady()
	host.write("{\"id\":1}\n")

	require.Equal(t, "{\"id\":1}\n", conn.readLine(), "the session is relaying")
	info, err := os.Stat(lockPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0), info.Mode().Perm(), "the lock file is left untouched")
}
