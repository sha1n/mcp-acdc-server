package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

type stubUIBackend struct{}

func (stubUIBackend) SearchLabeled(string, []string, int) ([]search.SearchResult, error) {
	return []search.SearchResult{}, nil
}

func (stubUIBackend) Labels(string, int) ([]search.LabelCount, error) {
	return []search.LabelCount{}, nil
}

func newUITestHandler(t *testing.T, ui config.UISettings, authSettings config.AuthSettings) http.Handler {
	t.Helper()
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	settings := &config.Settings{Host: "localhost", Port: 0, Transport: "sse", Auth: authSettings, UI: ui}

	srv, err := NewSSEServer(mcpSrv, settings, WithUIBackend(stubUIBackend{}))

	require.NoError(t, err)
	return srv.Handler
}

func get(handler http.Handler, path string, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestNewSSEServer_UIEnabledServesUIAndAPI(t *testing.T) {
	handler := newUITestHandler(t, config.UISettings{Enabled: true, MaxResults: 20}, config.AuthSettings{Type: config.AuthTypeNone})

	ui := get(handler, "/ui/", nil)
	require.Equal(t, http.StatusOK, ui.Code)
	require.Contains(t, ui.Body.String(), "<html")

	redirect := get(handler, "/ui", nil)
	require.GreaterOrEqual(t, redirect.Code, 300)
	require.Less(t, redirect.Code, 400)
	require.Equal(t, "/ui/", redirect.Header().Get("Location"))

	cfg := get(handler, "/api/v1/config", nil)
	require.Equal(t, http.StatusOK, cfg.Code)
	require.JSONEq(t, `{"default_limit":20,"max_limit":100}`, cfg.Body.String())

	require.Equal(t, http.StatusOK, get(handler, "/api/v1/labels", nil).Code)
	require.Equal(t, http.StatusOK, get(handler, "/api/v1/search?q=x", nil).Code)
}

func TestNewSSEServer_UIDisabledRoutesAreNotFound(t *testing.T) {
	handler := newUITestHandler(t, config.UISettings{Enabled: false, MaxResults: 20}, config.AuthSettings{Type: config.AuthTypeNone})

	for _, path := range []string{"/ui/", "/ui", "/api/v1/config", "/api/v1/labels", "/api/v1/search?q=x"} {
		require.Equal(t, http.StatusNotFound, get(handler, path, nil).Code, path)
	}
	require.Equal(t, http.StatusOK, get(handler, "/health", nil).Code)
}

func TestNewSSEServer_UIRoutesRequireAuthAndHealthDoesNot(t *testing.T) {
	handler := newUITestHandler(t,
		config.UISettings{Enabled: true, MaxResults: 20},
		config.AuthSettings{Type: config.AuthTypeAPIKey, APIKeys: []string{"secret"}},
	)

	health := get(handler, "/health", nil)
	require.Equal(t, http.StatusOK, health.Code)
	require.Equal(t, "ok", health.Body.String())

	for _, path := range []string{"/ui/", "/api/v1/config", "/api/v1/labels", "/api/v1/search?q=x"} {
		require.Equal(t, http.StatusUnauthorized, get(handler, path, nil).Code, path)
	}

	authed := http.Header{"X-Api-Key": []string{"secret"}}
	require.Equal(t, http.StatusOK, get(handler, "/ui/", authed).Code)
	require.Equal(t, http.StatusOK, get(handler, "/api/v1/config", authed).Code)
	require.Equal(t, http.StatusOK, get(handler, "/api/v1/labels", authed).Code)
	require.Equal(t, http.StatusOK, get(handler, "/api/v1/search?q=x", authed).Code)
}

func TestRegisterFlags_UIFlags(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NotNil(t, flags.Lookup("ui-enabled"))
	require.NotNil(t, flags.Lookup("ui-max-results"))

	require.NoError(t, flags.Set("ui-enabled", "true"))
	require.NoError(t, flags.Set("ui-max-results", "42"))
	settings, err := config.LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.True(t, settings.UI.Enabled)
	require.Equal(t, 42, settings.UI.MaxResults)
}
