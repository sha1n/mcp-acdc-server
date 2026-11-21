//go:build unix

package testkit

import (
	"errors"
	"net"

	"github.com/sha1n/mcp-acdc-server/internal/app"
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

// BrowseResult is how one browse command ended and what it wrote.
type BrowseResult struct {
	Stdout string
	Stderr string
	Err    error
}

// Browse runs a browse command with args in the test process; a shared
// server it starts is spawned in-process with the same args.
func (h *SharedServerHarness) Browse(args ...string) BrowseResult {
	h.t.Helper()
	flags := LaunchFlags(h.t, args)
	params := h.SessionProxyParams(args)
	var stdout, stderr lockedBuffer
	params.Stdout, params.Stderr = &stdout, &stderr
	err := app.RunBrowse(h.ctx, params, flags, h.Binary)
	return BrowseResult{Stdout: stdout.String(), Stderr: stderr.String(), Err: err}
}
