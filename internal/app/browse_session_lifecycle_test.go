//go:build unix

package app

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// browseLifecycleTestWait outlasts the default foreground shutdown timeout,
// so a browse that waits for in-flight handlers still returns in time.
const browseLifecycleTestWait = 15 * time.Second

func TestRunBrowse_SessionModeInterruptedDuringTheBuildExitsQuietly(t *testing.T) {
	tests := []struct {
		name  string
		build func(ctx context.Context) (Components, error)
	}{
		{
			name: "build returns the cancellation",
			build: func(ctx context.Context) (Components, error) {
				return Components{}, ctx.Err()
			},
		},
		{
			name: "build succeeds after the cancellation",
			build: func(context.Context) (Components, error) {
				return Components{
					MCP:        mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil),
					SSEOptions: []SSEOption{WithWebUI(stubUIBackend{}, stubUIDocuments{})},
				}, nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanups := 0
			stdout := &lockedBuffer{}
			params := browseSessionTestParams(t, browseSessionTestSettings(t), stdout, &cleanups)
			ctx, cancel := context.WithTimeout(context.Background(), browseSessionTestWait)
			defer cancel()
			params.CreateServer = func(buildCtx context.Context, _ *slog.Logger, _ *config.Settings, _ string) (Components, error) {
				cancel()
				<-buildCtx.Done()
				return tt.build(buildCtx)
			}
			params.OpenBrowser = func(string) error {
				t.Error("the browser must not be opened after an interrupt")
				return nil
			}

			err := RunBrowse(ctx, params, browseTestFlags(t), sessionProxyTestBinary)

			require.NoError(t, err, "an interrupt is not a failure")
			assert.Empty(t, stdout.String(), "no URL is printed after an interrupt")
		})
	}
}

func TestRunBrowse_SessionModeCancelWaitsForInFlightHandlersBeforeCleanup(t *testing.T) {
	run := startBrowseLifecycle(t, nil)
	run.awaitInFlightRequest(t)

	run.cancel()

	require.NoError(t, run.await(t))
	run.requireCleanupAfterHandler(t)
}

func TestRunBrowse_SessionModeServeFailureWaitsForInFlightHandlersBeforeCleanup(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	run := startBrowseLifecycle(t, listener)
	run.awaitInFlightRequest(t)

	require.NoError(t, listener.Close())

	require.Error(t, run.await(t), "a serve failure is a failure")
	run.requireCleanupAfterHandler(t)
}

func TestRunBrowse_SessionModeHandlerOutlastingTheShutdownGraceDelaysCleanup(t *testing.T) {
	const grace = 100 * time.Millisecond
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseHandler := func() { releaseOnce.Do(func() { close(release) }) }
	run := startBrowseLifecycleWith(t, nil, &blockingUIBackend{entered: make(chan struct{}), release: release}, grace)
	t.Cleanup(releaseHandler)
	run.awaitInFlightRequest(t)

	run.cancel()

	select {
	case err := <-run.done:
		run.done <- err
		t.Fatalf("RunBrowse returned while a request handler was still running: %v", err)
	case <-time.After(5 * grace):
	}
	assert.Zero(t, run.cleanups.Load(), "Cleanup ran while a request handler was still running")
	releaseHandler()
	require.NoError(t, run.await(t))
	run.requireCleanupAfterHandler(t)
}

// blockingUIBackend holds a labels request until its request context ends,
// then takes a moment to return, like a handler finishing its work. With a
// release channel it ignores its request context and holds the request until
// the channel closes.
type blockingUIBackend struct {
	stubUIBackend
	enterOnce sync.Once
	entered   chan struct{}
	release   chan struct{}
	returned  atomic.Bool
}

func (b *blockingUIBackend) Labels(ctx context.Context, _ string, _ int) ([]search.LabelCount, error) {
	b.enterOnce.Do(func() { close(b.entered) })
	if b.release != nil {
		<-b.release
		b.returned.Store(true)
		return nil, nil
	}
	<-ctx.Done()
	time.Sleep(100 * time.Millisecond)
	b.returned.Store(true)
	return nil, ctx.Err()
}

type browseLifecycleRun struct {
	backend          *blockingUIBackend
	stdout           *lockedBuffer
	cancel           context.CancelFunc
	done             chan error
	cleanups         atomic.Int32
	cleanupAfterDone atomic.Bool
}

func startBrowseLifecycle(t *testing.T, listener net.Listener) *browseLifecycleRun {
	t.Helper()
	return startBrowseLifecycleWith(t, listener, &blockingUIBackend{entered: make(chan struct{})}, 0)
}

// startBrowseLifecycleWith starts a session-mode browse; a zero grace keeps
// the default shutdown grace period.
func startBrowseLifecycleWith(t *testing.T, listener net.Listener, backend *blockingUIBackend, grace time.Duration) *browseLifecycleRun {
	t.Helper()
	run := &browseLifecycleRun{
		backend: backend,
		stdout:  &lockedBuffer{},
		done:    make(chan error, 1),
	}
	unused := 0
	params := browseSessionTestParams(t, browseSessionTestSettings(t), run.stdout, &unused)
	params.UIShutdownGrace = grace
	params.CreateServer = func(context.Context, *slog.Logger, *config.Settings, string) (Components, error) {
		return Components{
			MCP:        mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil),
			SSEOptions: []SSEOption{WithWebUI(run.backend, stubUIDocuments{})},
			Cleanup: func() {
				run.cleanups.Add(1)
				run.cleanupAfterDone.Store(run.backend.returned.Load())
			},
		}, nil
	}
	if listener != nil {
		params.ListenWebUI = func() (net.Listener, error) { return listener, nil }
	}
	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	go func() { run.done <- RunBrowse(ctx, params, browseTestFlags(t, "--no-open"), sessionProxyTestBinary) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-run.done:
			t.Logf("RunBrowse returned: %v", err)
		case <-time.After(browseLifecycleTestWait):
			t.Error("RunBrowse did not return after its context was cancelled")
		}
	})
	return run
}

// awaitInFlightRequest starts a labels request and returns once its handler
// is running.
func (r *browseLifecycleRun) awaitInFlightRequest(t *testing.T) {
	t.Helper()
	url := strings.TrimSuffix(strings.TrimSuffix(r.awaitURL(t), "\n"), "/ui/") + "/api/v1/labels"
	go func() {
		client := &http.Client{Timeout: browseLifecycleTestWait}
		if resp, err := client.Get(url); err == nil { //nolint:gosec // the loopback URL printed by browse
			_ = resp.Body.Close()
		}
	}()
	select {
	case <-r.backend.entered:
	case <-time.After(browseSessionTestWait):
		t.Fatal("the labels request did not reach its handler")
	}
}

func (r *browseLifecycleRun) awaitURL(t *testing.T) string {
	t.Helper()
	return awaitBrowseURL(t, r.stdout, r.done)
}

func (r *browseLifecycleRun) await(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.done:
		r.done <- err
		return err
	case <-time.After(browseLifecycleTestWait):
		t.Fatal("RunBrowse did not return")
		return nil
	}
}

func (r *browseLifecycleRun) requireCleanupAfterHandler(t *testing.T) {
	t.Helper()
	require.Equal(t, int32(1), r.cleanups.Load(), "the built components are cleaned up once")
	assert.True(t, r.cleanupAfterDone.Load(), "Cleanup ran while a request handler was still in flight")
}
