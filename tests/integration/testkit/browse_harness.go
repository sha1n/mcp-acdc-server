//go:build unix

package testkit

import (
	"errors"
	"net"
	"sync"

	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

// errUIBindRefused is what a Web UI bind failed by FailUIBinds returns.
var errUIBindRefused = errors.New("address already in use")

// FailUIBinds makes the next n Web UI binds of the shared servers the
// harness spawns fail, as when the port cannot be bound; later binds take an
// ephemeral port on 127.0.0.1.
func (h *SharedServerHarness) FailUIBinds(n int) { h.uiBindFailures.Store(int32(n)) }

func (h *SharedServerHarness) listenWebUI() (net.Listener, error) {
	if h.uiBindFailures.Add(-1) >= 0 {
		return nil, errUIBindRefused
	}
	return net.Listen("tcp", "127.0.0.1:0")
}

// BrowseResult is how one browse command ended, what it wrote and the URLs
// it opened the browser on.
type BrowseResult struct {
	Stdout string
	Stderr string
	Err    error
	Opened []string
}

// Browse runs a browse command with args in the test process, with a fake
// browser opener that records the URLs it is given. A shared server it
// starts is spawned in-process with the args a production browse passes.
func (h *SharedServerHarness) Browse(args ...string) BrowseResult {
	return h.BrowseOpeningWith(nil, args...)
}

// BrowseOpeningWith is Browse with a fake browser opener that fails with
// openErr after recording the URL.
func (h *SharedServerHarness) BrowseOpeningWith(openErr error, args ...string) BrowseResult {
	h.t.Helper()
	flags := pflag.NewFlagSet("browse", pflag.ContinueOnError)
	app.RegisterBrowseFlags(flags)
	require.NoError(h.t, flags.Parse(args))
	params := h.SessionProxyParams(app.BrowseSharedServerArgs(flags))
	var stdout, stderr lockedBuffer
	params.Stdout, params.Stderr = &stdout, &stderr
	var mu sync.Mutex
	var opened []string
	params.OpenBrowser = func(url string) error {
		mu.Lock()
		defer mu.Unlock()
		opened = append(opened, url)
		return openErr
	}
	err := app.RunBrowse(h.ctx, params, flags, h.Binary)
	mu.Lock()
	defer mu.Unlock()
	return BrowseResult{Stdout: stdout.String(), Stderr: stderr.String(), Err: err, Opened: opened}
}
