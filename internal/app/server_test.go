package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/sha1n/mcp-acdc-server/internal/webui"
	"github.com/stretchr/testify/require"
)

func TestNewSSEServer(t *testing.T) {
	tests := []struct {
		name     string
		settings *config.Settings
		wantErr  bool
		wantAddr string
	}{
		{
			name: "no auth",
			settings: &config.Settings{
				Host: "localhost",
				Port: 0,
				Auth: config.AuthSettings{Type: config.AuthTypeNone},
			},
			wantErr:  false,
			wantAddr: "localhost:0",
		},
		{
			name: "API key auth",
			settings: &config.Settings{
				Host: "localhost",
				Port: 0,
				Auth: config.AuthSettings{
					Type:    config.AuthTypeAPIKey,
					APIKeys: []string{"test-key"},
				},
			},
			wantErr: false,
		},
		{
			name: "basic auth",
			settings: &config.Settings{
				Host: "localhost",
				Port: 0,
				Auth: config.AuthSettings{
					Type: config.AuthTypeBasic,
					Basic: config.BasicAuthSettings{
						Username: "user",
						Password: "password",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid auth type",
			settings: &config.Settings{
				Host: "localhost",
				Port: 0,
				Auth: config.AuthSettings{Type: "invalid"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
			srv, err := NewSSEServer(mcpSrv, tt.settings)

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("NewSSEServer failed: %v", err)
			}
			if srv == nil {
				t.Fatal("Expected non-nil server")
			}
			if tt.wantAddr != "" && srv.Addr != tt.wantAddr {
				t.Errorf("Expected addr %s, got %s", tt.wantAddr, srv.Addr)
			}
		})
	}
}

func TestStartSSEServer_NewSSEServerError(t *testing.T) {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	settings := &config.Settings{
		Auth: config.AuthSettings{Type: "invalid"},
	}
	err := StartSSEServer(mcpSrv, settings)
	if err == nil {
		t.Error("Expected error for invalid auth type")
	}
}

func TestStartSSEServer_PortCollision(t *testing.T) {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)

	// Bind to a port
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Skip("Failed to bind to local port for test")
	}
	defer func() { _ = l.Close() }()
	port := l.Addr().(*net.TCPAddr).Port

	settings := &config.Settings{
		Host: "localhost",
		Port: port,
		Auth: config.AuthSettings{Type: config.AuthTypeNone},
	}

	err = StartSSEServer(mcpSrv, settings)
	if err == nil {
		t.Error("Expected error because port is already in use")
	}
}

type stubUIBackend struct{}

func (stubUIBackend) LabelledSearch(context.Context, string, []string, int) ([]search.LabelledResult, error) {
	return nil, nil
}

func (stubUIBackend) Labels(context.Context, string, int) ([]search.LabelCount, error) {
	return nil, nil
}

type stubUIDocuments struct{}

func (stubUIDocuments) Snapshot(context.Context) webui.DocumentSnapshot { return stubUISnapshot{} }

type stubUISnapshot struct{}

func (stubUISnapshot) Document(string) (webui.Document, error) {
	return webui.Document{}, webui.ErrDocumentNotFound
}

func (stubUISnapshot) ResolvePath(string, string) (string, bool) { return "", false }
func (stubUISnapshot) ResolveURI(string) (string, bool)          { return "", false }

type uiAuthCase struct {
	name      string
	auth      config.AuthSettings
	authorize func(*http.Request)
}

func uiAuthCases() []uiAuthCase {
	return []uiAuthCase{
		{
			name:      "none",
			auth:      config.AuthSettings{Type: config.AuthTypeNone},
			authorize: func(*http.Request) {},
		},
		{
			name: "basic",
			auth: config.AuthSettings{Type: config.AuthTypeBasic, Basic: config.BasicAuthSettings{Username: "user", Password: "password"}},
			authorize: func(r *http.Request) {
				r.SetBasicAuth("user", "password")
			},
		},
		{
			name: "apikey",
			auth: config.AuthSettings{Type: config.AuthTypeAPIKey, APIKeys: []string{"test-key"}},
			authorize: func(r *http.Request) {
				r.Header.Set("X-API-Key", "test-key")
			},
		},
	}
}

func serveSSE(t *testing.T, srv *http.Server, path string, authorize func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	authorize(req)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	return rec
}

func newUISSEServer(t *testing.T, auth config.AuthSettings, enabled bool) *http.Server {
	t.Helper()
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	settings := &config.Settings{
		Host: "localhost",
		Auth: auth,
		UI:   config.UISettings{Enabled: enabled, MaxResults: 7},
	}
	srv, err := NewSSEServer(mcpSrv, settings, WithWebUI(stubUIBackend{}, stubUIDocuments{}))
	require.NoError(t, err)
	return srv
}

func TestNewSSEServer_WebUIRoutesFollowEnabledSwitchBehindAuth(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		for _, ac := range uiAuthCases() {
			t.Run(fmt.Sprintf("enabled=%t/auth=%s", enabled, ac.name), func(t *testing.T) {
				srv := newUISSEServer(t, ac.auth, enabled)
				wantUI := http.StatusNotFound
				if enabled {
					wantUI = http.StatusOK
				}

				for _, path := range []string{"/ui/", "/api/v1/config"} {
					rec := serveSSE(t, srv, path, ac.authorize)
					require.Equal(t, wantUI, rec.Code, "authorized %s", path)

					if ac.auth.Type != config.AuthTypeNone {
						unauthorized := serveSSE(t, srv, path, func(*http.Request) {})
						require.Equal(t, http.StatusUnauthorized, unauthorized.Code, "unauthenticated %s", path)
					}
				}

				health := serveSSE(t, srv, "/health", func(*http.Request) {})
				require.Equal(t, http.StatusOK, health.Code)
				require.Equal(t, "ok", health.Body.String())
				require.Equal(t, "text/plain; charset=utf-8", health.Header().Get("Content-Type"))
			})
		}
	}
}

func TestNewSSEServer_WebUIEnabledServesMountedHandler(t *testing.T) {
	srv := newUISSEServer(t, config.AuthSettings{Type: config.AuthTypeNone}, true)
	noAuth := func(*http.Request) {}

	page := serveSSE(t, srv, "/ui/", noAuth)
	require.Equal(t, http.StatusOK, page.Code)
	require.Contains(t, page.Header().Get("Content-Type"), "text/html")
	require.NotEmpty(t, page.Header().Get("Content-Security-Policy"))

	redirect := serveSSE(t, srv, "/ui", noAuth)
	require.Equal(t, http.StatusMovedPermanently, redirect.Code)
	require.Equal(t, "/ui/", redirect.Header().Get("Location"))

	cfg := serveSSE(t, srv, "/api/v1/config", noAuth)
	require.Equal(t, http.StatusOK, cfg.Code)
	require.Equal(t, "application/json; charset=utf-8", cfg.Header().Get("Content-Type"))
	require.JSONEq(t, `{"default_limit":7,"max_limit":100}`, cfg.Body.String())
}

func TestNewSSEServer_WebUIDisabledRoutesAreNotFound(t *testing.T) {
	noAuth := func(*http.Request) {}
	tests := []struct {
		name string
		opts []SSEOption
	}{
		{name: "with ui components", opts: []SSEOption{WithWebUI(stubUIBackend{}, stubUIDocuments{})}},
		{name: "without ui components", opts: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
			settings := &config.Settings{Host: "localhost", Auth: config.AuthSettings{Type: config.AuthTypeNone}}

			srv, err := NewSSEServer(mcpSrv, settings, tt.opts...)

			require.NoError(t, err)
			for _, path := range []string{"/ui", "/ui/", "/ui/doc", "/api/v1/config", "/api/v1/search?q=x", "/api/v1/unknown"} {
				require.Equal(t, http.StatusNotFound, serveSSE(t, srv, path, noAuth).Code, path)
			}
			require.Equal(t, http.StatusOK, serveSSE(t, srv, "/health", noAuth).Code)
		})
	}
}

func TestNewSSEServer_WebUIEnabledWithoutComponentsFails(t *testing.T) {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	settings := &config.Settings{
		Host: "localhost",
		Auth: config.AuthSettings{Type: config.AuthTypeNone},
		UI:   config.UISettings{Enabled: true, MaxResults: 20},
	}

	srv, err := NewSSEServer(mcpSrv, settings)

	require.EqualError(t, err, "web UI enabled but no UI components supplied")
	require.Nil(t, srv)
}

func TestStartSSEServer_WebUIEnabledWithoutComponentsFails(t *testing.T) {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	settings := &config.Settings{
		Host: "localhost",
		Auth: config.AuthSettings{Type: config.AuthTypeNone},
		UI:   config.UISettings{Enabled: true, MaxResults: 20},
	}

	errCh := make(chan error, 1)
	go func() { errCh <- StartSSEServer(mcpSrv, settings) }()

	select {
	case err := <-errCh:
		require.ErrorContains(t, err, "web UI enabled but no UI components supplied")
	case <-time.After(2 * time.Second):
		require.Fail(t, "StartSSEServer started listening instead of rejecting the missing UI components")
	}
}
