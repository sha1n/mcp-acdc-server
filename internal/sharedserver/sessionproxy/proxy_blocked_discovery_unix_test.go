//go:build unix

package sessionproxy_test

import (
	"errors"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockedCall holds a synchronous callback of the session proxy until the
// test ends, as a slow identity walk or a slow spawn would.
type blockedCall struct {
	t       *testing.T
	entered chan struct{}
	release chan struct{}
}

func newBlockedCall(t *testing.T) *blockedCall {
	call := &blockedCall{t: t, entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() { close(call.release) })
	return call
}

func (c *blockedCall) block() {
	close(c.entered)
	<-c.release
}

func (c *blockedCall) awaitEntered() {
	c.t.Helper()
	select {
	case <-c.entered:
	case <-time.After(proxyTestDeadline):
		c.t.Fatal("the callback was never called")
	}
}

var endSessionWays = []struct {
	name string
	end  func(*testing.T, *proxyHost)
}{
	{name: "cancellation", end: func(_ *testing.T, h *proxyHost) { h.cancel() }},
	{name: "stdin EOF", end: func(t *testing.T, h *proxyHost) { require.NoError(t, h.stdin.Close()) }},
}

func TestRun_EndsPromptlyWhileTheIdentityIsBeingComputed(t *testing.T) {
	for _, way := range endSessionWays {
		t.Run(way.name, func(t *testing.T) {
			dir := proxyTestEndpointDir(t)
			server := newFakeSharedServer(t, dir)
			identity := newBlockedCall(t)
			host := startProxy(t, sessionproxy.Config{
				EndpointDir: fixedEndpointDir(dir),
				Spawn:       server.spawn,
				Identity: func() (string, error) {
					identity.block()
					return "", errors.New("late identity result")
				},
			})
			identity.awaitEntered()

			ended := time.Now()
			way.end(t, host)

			require.NoError(t, host.result())
			assert.Less(t, time.Since(ended), 500*time.Millisecond)
			assert.Zero(t, server.spawns.Load())
		})
	}
}

func TestRun_EndsPromptlyWhileASpawnIsInProgress(t *testing.T) {
	for _, way := range endSessionWays {
		t.Run(way.name, func(t *testing.T) {
			spawn := newBlockedCall(t)
			host := startProxy(t, sessionproxy.Config{
				EndpointDir: fixedEndpointDir(proxyTestEndpointDir(t)),
				Spawn: func(string) error {
					spawn.block()
					return errors.New("late spawn result")
				},
			})
			spawn.awaitEntered()

			ended := time.Now()
			way.end(t, host)

			require.NoError(t, host.result())
			assert.Less(t, time.Since(ended), 500*time.Millisecond)
		})
	}
}
