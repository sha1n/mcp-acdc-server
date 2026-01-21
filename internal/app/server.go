package app

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/mark3labs/mcp-go/server"
	"github.com/sha1n/mcp-acdc-server-go/internal/auth"
	"github.com/sha1n/mcp-acdc-server-go/internal/config"
)

// StartSSEServer starts the SSE server with authentication
func StartSSEServer(s *server.MCPServer, settings *config.Settings) error {
	srv, err := NewSSEServer(s, settings)
	if err != nil {
		return err
	}

	slog.Info("Server listening (HTTP)", "addr", srv.Addr, "auth_type", settings.Auth.Type)
	return srv.ListenAndServe()
}

// NewSSEServer creates a new SSE server with authentication middleware
func NewSSEServer(s *server.MCPServer, settings *config.Settings) (*http.Server, error) {
	sseServer := server.NewSSEServer(s)

	authMiddleware, err := auth.NewMiddleware(settings.Auth)
	if err != nil {
		return nil, fmt.Errorf("failed to create auth middleware: %w", err)
	}

	handler := authMiddleware(sseServer)
	addr := fmt.Sprintf("%s:%d", settings.Host, settings.Port)

	return &http.Server{
		Addr:    addr,
		Handler: handler,
	}, nil
}
