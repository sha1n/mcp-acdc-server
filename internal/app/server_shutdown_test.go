package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

const (
	sseShutdownStartedMsg = "Shutting down SSE server"
	sseShutdownStoppedMsg = "SSE server stopped"
	// promptShutdown is well under DefaultShutdownGrace, so a return within
	// it proves the server did not wait out the grace.
	promptShutdown  = time.Second
	sseListeningMsg = "Server listening (HTTP)"
	readyTimeout    = 5 * time.Second
)

func TestStartSSEServer_ReturnsNilPromptlyWhenContextCancelled(t *testing.T) {
	settings := sseTestSettings(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := startSSEForTest(ctx, slog.New(slog.DiscardHandler), settings)
	awaitSSEHealthy(t, settings, done)

	cancel()

	require.NoError(t, awaitSSEReturn(t, done, promptShutdown))
}

func TestStartSSEServer_EndsOpenStreamAndReturnsPromptlyWhenContextCancelled(t *testing.T) {
	settings := sseTestSettings(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := startSSEForTest(ctx, slog.New(slog.DiscardHandler), settings)
	awaitSSEHealthy(t, settings, done)
	stream := openSSEStream(t, settings)

	cancel()

	require.NoError(t, awaitSSEReturn(t, done, promptShutdown))
	select {
	case <-stream.ended:
	case <-time.After(promptShutdown):
		require.Fail(t, "the open /sse stream was not ended by the shutdown")
	}
}

func TestStartSSEServer_ForceClosesHandlerIgnoringCancellationAfterGrace(t *testing.T) {
	const grace = 300 * time.Millisecond
	settings := sseTestSettings(t)
	settings.UI = config.UISettings{Enabled: true, MaxResults: 10}
	backend := newCancellationIgnoringBackend(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := startSSEForTest(ctx, slog.New(slog.DiscardHandler), settings,
		WithWebUI(backend, stubUIDocuments{}), withShutdownGrace(grace))
	awaitSSEHealthy(t, settings, done)
	requestErr := make(chan error, 1)
	client := sseTestClient(t)
	go func() {
		resp, err := client.Get(sseURL(settings, "/api/v1/search?q=stuck"))
		if err == nil {
			_, err = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
		}
		requestErr <- err
	}()
	select {
	case <-backend.entered:
	case <-time.After(readyTimeout):
		require.FailNow(t, "the search request never reached the backend")
	}

	started := time.Now()
	cancel()

	require.NoError(t, awaitSSEReturn(t, done, grace+promptShutdown))
	require.GreaterOrEqual(t, time.Since(started), grace, "the in-flight handler was not given the grace")
	select {
	case err := <-requestErr:
		require.Error(t, err, "the stuck request's connection was not force-closed")
	case <-time.After(promptShutdown):
		require.Fail(t, "the stuck request's connection was not force-closed")
	}
}

func TestStartSSEServer_LogsShutdownStartAndStopOnce(t *testing.T) {
	settings := sseTestSettings(t)
	records := &recordingHandler{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := startSSEForTest(ctx, slog.New(records), settings)
	awaitSSEHealthy(t, settings, done)

	cancel()

	require.NoError(t, awaitSSEReturn(t, done, promptShutdown))
	require.Equal(t, 1, records.count(slog.LevelInfo, sseShutdownStartedMsg))
	require.Equal(t, 1, records.count(slog.LevelInfo, sseShutdownStoppedMsg))
}

// A cancelled run is a clean stop, so it returns nil, and it must not bind:
// the port is held by another listener here, so any bind attempt would fail.
func TestStartSSEServer_ReturnsNilWithoutListeningWhenContextAlreadyCancelled(t *testing.T) {
	settings := sseTestSettings(t)
	occupyPort(t, settings)
	records := &recordingHandler{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := startSSEForTest(ctx, slog.New(records), settings)

	require.NoError(t, awaitSSEReturn(t, done, promptShutdown))
	require.Zero(t, records.count(slog.LevelInfo, sseListeningMsg), "a cancelled run must not report that it listens")
}

func TestServeSSEServer_ReturnsNilWithoutListeningWhenContextAlreadyCancelled(t *testing.T) {
	settings := sseTestSettings(t)
	occupyPort(t, settings)
	logger := slog.New(slog.DiscardHandler)
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	srv, err := NewSSEServer(logger, mcpSrv, settings)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- ServeSSEServer(ctx, logger, srv) }()

	require.NoError(t, awaitSSEReturn(t, done, promptShutdown))
}

func occupyPort(t *testing.T, settings *config.Settings) {
	t.Helper()
	l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", settings.Host, settings.Port))
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
}

func TestRunWithDeps_SSEReturnsNilAndCleansUpWhenContextCancelled(t *testing.T) {
	settings := sseTestSettings(t)
	settings.Transport = "sse"
	var cleaned atomic.Bool
	params := RunParams{
		Stderr:         io.Discard,
		LoadSettings:   func(*pflag.FlagSet) (*config.Settings, error) { return settings, nil },
		ValidSettings:  noopValidate,
		StartSSEServer: StartSSEServer,
		CreateServer: func(context.Context, *slog.Logger, *config.Settings, string) (Components, error) {
			return Components{
				MCP:     mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil),
				Cleanup: func() { cleaned.Store(true) },
			}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunWithDeps(ctx, params, nil, identity.Binary{Version: "test"}) }()
	awaitSSEHealthy(t, settings, done)

	cancel()

	require.NoError(t, awaitSSEReturn(t, done, promptShutdown))
	require.True(t, cleaned.Load(), "components.Cleanup did not run")
}

func sseTestSettings(t *testing.T) *config.Settings {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return &config.Settings{
		Host: "127.0.0.1",
		Port: port,
		Auth: config.AuthSettings{Type: config.AuthTypeNone},
	}
}

func startSSEForTest(ctx context.Context, logger *slog.Logger, settings *config.Settings, opts ...SSEOption) <-chan error {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	done := make(chan error, 1)
	go func() { done <- StartSSEServer(ctx, logger, mcpSrv, settings, opts...) }()
	return done
}

func sseURL(settings *config.Settings, path string) string {
	return fmt.Sprintf("http://%s:%d%s", settings.Host, settings.Port, path)
}

func sseTestClient(t *testing.T) *http.Client {
	t.Helper()
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}

// awaitSSEHealthy polls /health until the server answers 200 unauthenticated,
// failing fast if the server returns first.
func awaitSSEHealthy(t *testing.T, settings *config.Settings, done <-chan error) {
	t.Helper()
	client := sseTestClient(t)
	client.Timeout = time.Second
	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			require.FailNowf(t, "server returned before it was ready", "error: %v", err)
		default:
		}
		resp, err := client.Get(sseURL(settings, "/health"))
		if err == nil {
			_ = resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.FailNow(t, "server never answered /health")
}

func awaitSSEReturn(t *testing.T, done <-chan error, within time.Duration) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(within):
		require.FailNowf(t, "server did not return after its context was cancelled", "waited %s", within)
		return nil
	}
}

type sseStream struct {
	ended chan struct{}
}

// openSSEStream opens /sse, waits for its first event line, then drains it in
// the background; ended closes when the server ends the stream.
func openSSEStream(t *testing.T, settings *config.Settings) *sseStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sseURL(settings, "/sse"), nil)
	require.NoError(t, err)
	resp, err := sseTestClient(t).Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, http.StatusOK, resp.StatusCode)

	reader := bufio.NewReader(resp.Body)
	firstLine := make(chan error, 1)
	go func() {
		_, err := reader.ReadString('\n')
		firstLine <- err
	}()
	select {
	case err := <-firstLine:
		require.NoError(t, err)
	case <-time.After(readyTimeout):
		require.FailNow(t, "the /sse stream sent no event")
	}

	stream := &sseStream{ended: make(chan struct{})}
	go func() {
		_, _ = io.Copy(io.Discard, reader)
		close(stream.ended)
	}()
	return stream
}

// cancellationIgnoringBackend blocks every search until the test ends,
// regardless of its request context.
type cancellationIgnoringBackend struct {
	stubUIBackend
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newCancellationIgnoringBackend(t *testing.T) *cancellationIgnoringBackend {
	b := &cancellationIgnoringBackend{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() { close(b.release) })
	return b
}

func (b *cancellationIgnoringBackend) LabelledSearch(context.Context, string, []string, int) ([]search.LabelledResult, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return nil, nil
}

type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

func (h *recordingHandler) count(level slog.Level, msg string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, r := range h.records {
		if r.Level == level && r.Message == msg {
			n++
		}
	}
	return n
}
