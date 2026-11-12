package app

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/auth"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/webui"
)

type sseOptions struct {
	uiBackend   webui.SearchBackend
	uiDocuments webui.DocumentReader
}

// SSEOption configures optional SSE server capabilities.
type SSEOption func(*sseOptions)

// WithWebUI supplies the components the web UI serves when it is enabled.
func WithWebUI(backend webui.SearchBackend, documents webui.DocumentReader) SSEOption {
	return func(o *sseOptions) {
		o.uiBackend = backend
		o.uiDocuments = documents
	}
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
	if settings.UI.Enabled {
		if options.uiBackend == nil || options.uiDocuments == nil {
			return nil, errors.New("web UI enabled but no UI components supplied")
		}
		webui.Mount(mux, options.uiBackend, options.uiDocuments, settings.UI)
	}

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
