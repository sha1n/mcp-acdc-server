//go:build unix

package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A signal that arrives as the build completes may let the server bind
// briefly; the guaranteed outcome is a clean stop: the server, if started,
// gets a context that ends, the run returns nil and the components are cleaned
// up.
func TestRunWithDeps_SignalDuringBuildStopsCleanly(t *testing.T) {
	// Keeps the test binary alive if the run does not handle the signal.
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, syscall.SIGTERM)
	t.Cleanup(func() { signal.Stop(guard) })

	const watchdog = 10 * time.Second
	ctx := context.WithValue(context.Background(), runnerContextKey{}, "caller")
	var createServerCtx context.Context
	var cleanupCalls atomic.Int32
	params := RunParams{
		Stderr:        io.Discard,
		LoadSettings:  func(*pflag.FlagSet) (*config.Settings, error) { return &config.Settings{Transport: "sse"}, nil },
		ValidSettings: noopValidate,
		CreateServer: func(gotCtx context.Context, _ *slog.Logger, _ *config.Settings, _ string) (Components, error) {
			createServerCtx = gotCtx
			require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
			select {
			case <-guard:
			case <-time.After(watchdog):
				t.Error("SIGTERM was never delivered")
			}
			return Components{Cleanup: func() { cleanupCalls.Add(1) }}, nil
		},
		StartSSEServer: func(serveCtx context.Context, _ *slog.Logger, _ *mcp.Server, _ *config.Settings, _ ...SSEOption) error {
			select {
			case <-serveCtx.Done():
			case <-time.After(watchdog):
				t.Error("the server context did not end after a signal during the build")
			}
			return nil
		},
	}

	done := make(chan error, 1)
	go func() { done <- RunWithDeps(ctx, params, nil, identity.Binary{Version: "test"}) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * watchdog):
		t.Fatal("RunWithDeps did not return after a signal during the build")
	}
	assert.Same(t, ctx, createServerCtx, "CreateServer must receive the exact caller context")
	assert.Equal(t, int32(1), cleanupCalls.Load(), "a signal during the build must still clean up the components")
}
