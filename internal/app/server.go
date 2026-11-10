package app

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/auth"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/webui"
)

// SSEOption customises NewSSEServer.
type SSEOption func(*sseOptions)

type sseOptions struct {
	uiBackend   webui.Backend
	uiDocuments webui.DocumentReader
}

// WithUIBackend supplies the backend for the web UI. Without one the UI routes
// are not registered even when ui.enabled is set.
func WithUIBackend(backend webui.Backend) SSEOption {
	return func(o *sseOptions) { o.uiBackend = backend }
}

// WithUIDocumentReader supplies the whole-document reader behind
// /api/v1/document. Without one that route returns 404.
func WithUIDocumentReader(reader webui.DocumentReader) SSEOption {
	return func(o *sseOptions) { o.uiDocuments = reader }
}

// uiComponents are the web UI dependencies createServer builds alongside the
// MCP server.
type uiComponents struct {
	backend   webui.Backend
	documents webui.DocumentReader
}

func (c uiComponents) sseOptions() []SSEOption {
	return []SSEOption{WithUIBackend(c.backend), WithUIDocumentReader(c.documents)}
}

// StartSSEServer starts the SSE server with authentication
func StartSSEServer(s *mcp.Server, settings *config.Settings, opts ...SSEOption) error {
	srv, err := NewSSEServer(s, settings, opts...)
	if err != nil {
		return err
	}

	slog.Info("Server listening (HTTP)", "addr", srv.Addr, "auth_type", settings.Auth.Type)
	return srv.ListenAndServe()
}

// NewSSEServer creates a new SSE server with authentication middleware
func NewSSEServer(s *mcp.Server, settings *config.Settings, opts ...SSEOption) (*http.Server, error) {
	var options sseOptions
	for _, opt := range opts {
		opt(&options)
	}

	// Factory function returns the server instance for each request
	sseHandler := mcp.NewSSEHandler(func(r *http.Request) *mcp.Server {
		return s
	}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/sse", sseHandler)
	mountUI(mux, settings.UI, options.uiBackend, options.uiDocuments)

	authMiddleware, err := auth.NewMiddleware(settings.Auth)
	if err != nil {
		return nil, fmt.Errorf("failed to create auth middleware: %w", err)
	}

	handler := authMiddleware(mux)
	addr := fmt.Sprintf("%s:%d", settings.Host, settings.Port)

	return &http.Server{
		Addr:    addr,
		Handler: handler,
	}, nil
}

func mountUI(mux *http.ServeMux, ui config.UISettings, backend webui.Backend, documents webui.DocumentReader) {
	if !ui.Enabled {
		return
	}
	if backend == nil {
		slog.Warn("Web UI is enabled but no search backend is available; UI not served")
		return
	}
	var opts []webui.HandlerOption
	if documents != nil {
		opts = append(opts, webui.WithDocumentReader(documents))
	}
	uiHandler := webui.NewHandler(backend, ui, opts...)
	for _, pattern := range []string{"/ui", "/ui/", "/api/v1/"} {
		mux.Handle(pattern, uiHandler)
	}
	slog.Info("Web UI enabled", "path", "/ui/")
}
