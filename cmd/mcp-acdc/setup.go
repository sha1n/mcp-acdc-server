package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/mark3labs/mcp-go/server"
	"github.com/sha1n/mcp-acdc-server-go/internal/auth"
	"github.com/sha1n/mcp-acdc-server-go/internal/config"
	"github.com/sha1n/mcp-acdc-server-go/internal/content"
	"github.com/sha1n/mcp-acdc-server-go/internal/domain"
	"github.com/sha1n/mcp-acdc-server-go/internal/mcp"
	"github.com/sha1n/mcp-acdc-server-go/internal/resources"
	"github.com/sha1n/mcp-acdc-server-go/internal/search"
	"gopkg.in/yaml.v3"
)

// StartSSEServer starts the SSE server with authentication
func StartSSEServer(s *server.MCPServer, settings *config.Settings) error {
	sseServer := server.NewSSEServer(s)

	authMiddleware, err := auth.NewMiddleware(settings.Auth)
	if err != nil {
		return fmt.Errorf("failed to create auth middleware: %w", err)
	}

	handler := authMiddleware(sseServer)

	addr := fmt.Sprintf("%s:%d", settings.Host, settings.Port)

	slog.Info("Server listening (HTTP)", "addr", addr, "auth_type", settings.Auth.Type)
	return http.ListenAndServe(addr, handler)
}

// CreateMCPServer initializes the core MCP server components
func CreateMCPServer(settings *config.Settings) (*server.MCPServer, func(), error) {
	// Initialize content provider
	cp := content.NewContentProvider(settings.ContentDir)

	// Load metadata
	metadataPath := cp.GetPath("mcp-metadata.yaml")

	mdBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read metadata file: %w", err)
	}

	var metadata domain.McpMetadata
	if err := yaml.Unmarshal(mdBytes, &metadata); err != nil {
		return nil, nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	if err := metadata.Validate(); err != nil {
		return nil, nil, fmt.Errorf("metadata validation failed: %w", err)
	}

	// Discover resources
	resourceDefinitions, err := resources.DiscoverResources(cp)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to discover resources: %w", err)
	}

	resourceProvider := resources.NewResourceProvider(resourceDefinitions)

	// Initialize search service
	searchService := search.NewService(settings.Search)
	cleanup := func() {
		searchService.Close()
	}

	// Index resources
	docsToIndex := resourceProvider.GetAllResourceContents()
	var docs []search.Document
	for _, d := range docsToIndex {
		docs = append(docs, search.Document{
			URI:     d["uri"],
			Name:    d["name"],
			Content: d["content"],
		})
	}

	if err := searchService.IndexDocuments(docs); err != nil {
		slog.Error("Failed to index documents", "error", err)
	} else if len(docs) > 0 {
		slog.Info("Indexed documents", "count", len(docs))
	}

	// Create MCP server
	mcpServer := mcp.CreateServer(metadata, resourceProvider, searchService)

	return mcpServer, cleanup, nil
}
