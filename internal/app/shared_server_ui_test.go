package app

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serveUI(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestNewWebUIHandler_ServesTheUIAndAPIWithTheRequestedMaxResults(t *testing.T) {
	handler, err := newWebUIHandler(slog.New(slog.DiscardHandler), []SSEOption{WithWebUI(stubUIBackend{}, stubUIDocuments{})}, 13)
	require.NoError(t, err)

	page := serveUI(t, handler, "/ui/")
	assert.Equal(t, http.StatusOK, page.Code)
	assert.Contains(t, page.Header().Get("Content-Type"), "text/html")

	cfg := serveUI(t, handler, "/api/v1/config")
	assert.Equal(t, http.StatusOK, cfg.Code)
	assert.JSONEq(t, `{"default_limit":13,"max_limit":100}`, cfg.Body.String())
}

func TestNewWebUIHandler_ServesNoSSEOrHealthEndpoint(t *testing.T) {
	handler, err := newWebUIHandler(slog.New(slog.DiscardHandler), []SSEOption{WithWebUI(stubUIBackend{}, stubUIDocuments{})}, 13)
	require.NoError(t, err)

	for _, path := range []string{"/sse", "/health"} {
		assert.Equal(t, http.StatusNotFound, serveUI(t, handler, path).Code, path)
	}
}

func TestNewWebUIHandler_RequiresUIComponents(t *testing.T) {
	_, err := newWebUIHandler(slog.New(slog.DiscardHandler), nil, 13)
	assert.Error(t, err)
}

func TestSharedServerComponents_UIHandlerServesTheBuiltComponents(t *testing.T) {
	built := &sharedServerComponents{
		settings: &config.Settings{},
		version:  "test",
		create: func(context.Context, *slog.Logger, *config.Settings, string) (Components, error) {
			return Components{
				MCP:        mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil),
				SSEOptions: []SSEOption{WithWebUI(stubUIBackend{}, stubUIDocuments{})},
			}, nil
		},
	}
	server, _, err := built.build(context.Background(), slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	require.NotNil(t, server)

	handler, err := built.uiHandler(21)
	require.NoError(t, err)

	cfg := serveUI(t, handler, "/api/v1/config")
	assert.JSONEq(t, `{"default_limit":21,"max_limit":100}`, cfg.Body.String())
}

func TestSharedServerComponents_RevalidateUsesTheBuiltComponents(t *testing.T) {
	revalidated := 0
	built := &sharedServerComponents{
		settings: &config.Settings{},
		version:  "test",
		create: func(context.Context, *slog.Logger, *config.Settings, string) (Components, error) {
			return Components{
				MCP:           mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil),
				RevalidateNow: func(context.Context) { revalidated++ },
			}, nil
		},
	}
	_, _, err := built.build(context.Background(), slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	built.revalidate(context.Background())

	assert.Equal(t, 1, revalidated)
}
