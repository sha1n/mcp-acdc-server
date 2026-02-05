package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/prompts"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"gopkg.in/yaml.v3"
)

// CreateMCPServer initializes the core MCP server components
func CreateMCPServer(settings *config.Settings) (*mcpsdk.Server, func(), error) {
	// Load config file
	mdBytes, err := os.ReadFile(settings.ConfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var metadata domain.McpMetadata
	if err := yaml.Unmarshal(mdBytes, &metadata); err != nil {
		return nil, nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if err := metadata.Validate(); err != nil {
		return nil, nil, fmt.Errorf("config validation failed: %w", err)
	}

	// Get config directory for relative path resolution
	configDir := filepath.Dir(settings.ConfigPath)

	// Initialize content provider with multiple locations
	cp, err := content.NewContentProvider(metadata.Content, configDir)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize content provider: %w", err)
	}

	// Discover resources from all locations
	resourceDefinitions, err := resources.DiscoverResources(cp.ResourceLocations(), cp)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to discover resources: %w", err)
	}

	resourceProvider := resources.NewResourceProvider(resourceDefinitions)

	// Discover prompts from all locations
	promptDefinitions, err := prompts.DiscoverPrompts(cp.PromptLocations(), cp)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to discover prompts: %w", err)
	}

	promptProvider := prompts.NewPromptProvider(promptDefinitions)

	// Initialize search service
	searchService := search.NewService(settings.Search)
	cleanup := func() {
		searchService.Close()
	}

	// Index resources
	IndexResources(context.Background(), resourceProvider, searchService)

	// Build enhanced instructions
	enhancedInstructions := buildInstructions(metadata.Server.Instructions, metadata.Content)

	// Create MCP server with enhanced instructions
	mcpServer := mcp.CreateServer(metadata, enhancedInstructions, resourceProvider, promptProvider, searchService)

	return mcpServer, cleanup, nil
}

// buildInstructions builds enhanced instructions with content source information
func buildInstructions(baseInstructions string, locations []domain.ContentLocation) string {
	if len(locations) == 0 {
		return baseInstructions
	}

	var sb strings.Builder
	sb.WriteString(baseInstructions)
	sb.WriteString("\n\nAvailable content sources:\n")
	for _, loc := range locations {
		fmt.Fprintf(&sb, "- %s: %s\n", loc.Name, loc.Description)
	}
	sb.WriteString("\nUse the search tool to find information. You can optionally filter by source.")
	return sb.String()
}
