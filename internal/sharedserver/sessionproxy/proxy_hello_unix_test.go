//go:build unix

package sessionproxy_test

import (
	"log/slog"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireHelloBeforeReady reads the first line of a connection the proxy just
// dialed, before any ready line was written, and requires the session hello.
func requireHelloBeforeReady(t *testing.T, conn *fakeSharedConn) {
	t.Helper()
	hello, err := sharedserver.DecodeHelloLine([]byte(conn.readLine()))
	require.NoError(t, err, "the first line on a connection must be the hello")
	assert.Equal(t, sharedserver.HelloSession, hello.Kind)
}

func TestRun_SendsTheHelloBeforeReadingTheReadyLineOnTheFirstConnect(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	host := startProxy(t, sessionproxy.Config{EndpointDir: fixedEndpointDir(dir), Spawn: server.spawn, Logger: slog.New(slog.DiscardHandler)})

	conn := server.nextRawConn()
	requireHelloBeforeReady(t, conn)

	conn.sendReady()
	host.write(request("1"))
	assert.Equal(t, request("1"), conn.readLine(), "JSON-RPC follows the hello")
}

func TestRun_SendsTheHelloBeforeReadingTheReadyLineOnEveryRecovery(t *testing.T) {
	proxy, old := startRecoveringProxy(t)

	for _, session := range []string{"second", "third"} {
		old.crash()
		recovered := proxy.server.nextRawConn()
		requireHelloBeforeReady(t, recovered)
		recovered.sendReadyAs(session)
		proxy.host.write(request(session))
		require.Equal(t, request(session), recovered.readLine(), "JSON-RPC follows the hello")
		old = recovered
	}
}
