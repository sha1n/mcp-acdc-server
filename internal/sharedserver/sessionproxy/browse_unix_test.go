//go:build unix

package sessionproxy_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type browseResult struct {
	ready sharedserver.ReadyLine
	err   error
}

func startBrowse(t *testing.T, cfg sessionproxy.BrowseConfig) <-chan browseResult {
	t.Helper()
	if cfg.Identity == nil {
		cfg.Identity = func() (string, error) { return proxyTestIdentity, nil }
	}
	if cfg.ConnectWait == 0 {
		cfg.ConnectWait = proxyTestDeadline
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	done := make(chan browseResult, 1)
	go func() {
		ready, err := sessionproxy.Browse(context.Background(), cfg)
		done <- browseResult{ready: ready, err: err}
	}()
	return done
}

func awaitBrowse(t *testing.T, done <-chan browseResult) browseResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(proxyTestDeadline):
		t.Fatal("browse did not return")
		return browseResult{}
	}
}

func browseConfig(dir string, server *fakeSharedServer) sessionproxy.BrowseConfig {
	return sessionproxy.BrowseConfig{EndpointDir: fixedEndpointDir(dir), Spawn: server.spawn, UIMaxResults: 7}
}

func (c *fakeSharedConn) requireBrowseHello() {
	c.t.Helper()
	hello, err := sharedserver.DecodeHelloLine([]byte(c.readLine()))
	require.NoError(c.t, err)
	assert.Equal(c.t, sharedserver.HelloBrowse, hello.Kind)
	assert.Equal(c.t, 7, hello.UIMaxResults)
}

func TestBrowse_SpawnsASharedServerAndReturnsTheURLOfItsReply(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	done := startBrowse(t, browseConfig(dir, server))

	conn := server.nextRawConn()
	conn.requireBrowseHello()
	line, err := sharedserver.EncodeBrowseReadyLine("http://127.0.0.1:1234/ui/", "/var/log/acdc.log")
	require.NoError(t, err)
	conn.write(string(line))

	r := awaitBrowse(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, "http://127.0.0.1:1234/ui/", r.ready.UIURL)
	assert.Equal(t, int32(1), server.spawns.Load())
}

func TestBrowse_ReturnsTheErrorTheSharedServerReplied(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	done := startBrowse(t, browseConfig(dir, server))

	conn := server.nextRawConn()
	conn.requireBrowseHello()
	line, err := sharedserver.EncodeBrowseErrorLine("address in use", "/var/log/acdc.log")
	require.NoError(t, err)
	conn.write(string(line))

	r := awaitBrowse(t, done)
	require.Error(t, r.err)
	assert.Contains(t, r.err.Error(), "address in use")
	assert.Equal(t, "/var/log/acdc.log", r.ready.Log)
}

func TestBrowse_ACloseWithoutAReplyIsRetriedOnceThenFails(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	done := startBrowse(t, browseConfig(dir, server))

	for range 2 {
		conn := server.nextRawConn()
		conn.requireBrowseHello()
		conn.crash()
	}

	r := awaitBrowse(t, done)
	require.Error(t, r.err)
	assert.Contains(t, r.err.Error(), "without returning the Web UI URL")
}

func TestBrowse_ACloseWithoutAReplyIsRecoveredByTheSecondAttempt(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	server := newFakeSharedServer(t, dir)
	done := startBrowse(t, browseConfig(dir, server))

	first := server.nextRawConn()
	first.requireBrowseHello()
	first.crash()
	second := server.nextRawConn()
	second.requireBrowseHello()
	line, err := sharedserver.EncodeBrowseReadyLine("http://127.0.0.1:1/ui/", "")
	require.NoError(t, err)
	second.write(string(line))

	r := awaitBrowse(t, done)
	require.NoError(t, r.err)
	assert.Equal(t, "http://127.0.0.1:1/ui/", r.ready.UIURL)
}

func TestBrowse_FailsWhenNoSharedServerIsReachableWithinTheConnectWait(t *testing.T) {
	dir := proxyTestEndpointDir(t)
	cfg := sessionproxy.BrowseConfig{
		EndpointDir:  fixedEndpointDir(dir),
		Spawn:        func(string) error { return nil },
		UIMaxResults: 7,
		ConnectWait:  200 * time.Millisecond,
	}

	r := awaitBrowse(t, startBrowse(t, cfg))

	require.ErrorIs(t, r.err, sessionproxy.ErrConnectWaitExpired)
}

func TestBrowse_FailsWhenTheIdentityCannotBeComputed(t *testing.T) {
	cfg := sessionproxy.BrowseConfig{
		EndpointDir:  fixedEndpointDir(proxyTestEndpointDir(t)),
		Identity:     func() (string, error) { return "", errors.New("walk failed") },
		Spawn:        func(string) error { return nil },
		UIMaxResults: 7,
	}

	r := awaitBrowse(t, startBrowse(t, cfg))

	require.Error(t, r.err)
	assert.Contains(t, r.err.Error(), "walk failed")
}

func TestBrowse_RejectsANonPositiveUIMaxResults(t *testing.T) {
	cfg := sessionproxy.BrowseConfig{
		EndpointDir: fixedEndpointDir(proxyTestEndpointDir(t)),
		Spawn:       func(string) error { return nil },
	}

	r := awaitBrowse(t, startBrowse(t, cfg))

	require.Error(t, r.err)
}
