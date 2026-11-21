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

func newSSEOptions(opts []SSEOption) sseOptions {
	var options sseOptions
	for _, opt := range opts {
		opt(&options)
	}
	return options
}

// mountWebUI mounts the web UI pages and API on mux; it is the one wiring of
// the web UI, for the SSE server and for a shared server's Web UI alike.
func (o sseOptions) mountWebUI(logger *slog.Logger, mux *http.ServeMux, ui config.UISettings) error {
	if o.uiBackend == nil || o.uiDocuments == nil {
		return errors.New("web UI enabled but no UI components supplied")
	}
	webui.Mount(logger, mux, o.uiBackend, o.uiDocuments, ui)
	return nil
}

// newWebUIHandler serves only the web UI pages and API, with no
// authentication and maxResults as its ui.max_results.
func newWebUIHandler(logger *slog.Logger, opts []SSEOption, maxResults int) (http.Handler, error) {
	mux := http.NewServeMux()
	ui := config.UISettings{Enabled: true, MaxResults: maxResults}
	if err := newSSEOptions(opts).mountWebUI(logger, mux, ui); err != nil {
		return nil, err
	}
	return mux, nil
}

// StartSSEServer starts the SSE server with authentication
func StartSSEServer(logger *slog.Logger, s *mcp.Server, settings *config.Settings, opts ...SSEOption) error {
	srv, err := NewSSEServer(logger, s, settings, opts...)
	if err != nil {
		return err
	}

	logger.Info("Server listening (HTTP)", "addr", srv.Addr, "auth_type", settings.Auth.Type)
	return srv.ListenAndServe()
}

// NewSSEServer creates a new SSE server with authentication middleware
func NewSSEServer(logger *slog.Logger, s *mcp.Server, settings *config.Settings, opts ...SSEOption) (*http.Server, error) {
	options := newSSEOptions(opts)

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
		if err := options.mountWebUI(logger, mux, settings.UI); err != nil {
			return nil, err
		}
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
