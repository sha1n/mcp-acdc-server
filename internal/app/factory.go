package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/adapters"
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

	// Create adapter registry with standard adapters
	registry := createAdapterRegistry()

	// Discover resources using adapter-based discovery
	resourceDefinitions, err := DiscoverResourcesWithAdapters(metadata.Content, cp, registry, configDir)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to discover resources: %w", err)
	}

	resourceProvider := resources.NewResourceProvider(resourceDefinitions)

	// Discover prompts using adapter-based discovery
	promptDefinitions, err := DiscoverPromptsWithAdapters(metadata.Content, cp, registry, configDir)
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

// createAdapterRegistry creates and initializes the adapter registry with standard adapters
func createAdapterRegistry() *adapters.Registry {
	registry := adapters.NewRegistry()
	registry.Register(adapters.NewACDCAdapter())
	registry.Register(adapters.NewLegacyAdapter())
	return registry
}

// DiscoverResourcesWithAdapters discovers resources using the adapter system
func DiscoverResourcesWithAdapters(locations []domain.ContentLocation, cp *content.ContentProvider, registry *adapters.Registry, configDir string) ([]resources.ResourceDefinition, error) {
	var allDefinitions []resources.ResourceDefinition

	for _, loc := range locations {
		// Get the resolved base path from content provider
		basePath, ok := cp.GetBasePath(loc.Name)
		if !ok {
			return nil, fmt.Errorf("content location %q: base path not found", loc.Name)
		}

		// Resolve adapter for this location
		var adapter adapters.Adapter
		if loc.Type != "" {
			// Explicit adapter type
			var ok bool
			adapter, ok = registry.Get(loc.Type)
			if !ok {
				return nil, fmt.Errorf("content location %q: unknown adapter type %q", loc.Name, loc.Type)
			}
		} else {
			// Auto-detect adapter
			var err error
			adapter, err = registry.AutoDetect(basePath)
			if err != nil {
				return nil, fmt.Errorf("content location %q: %w", loc.Name, err)
			}
		}

		// Create adapter location
		adapterLoc := adapters.Location{
			Name:        loc.Name,
			BasePath:    basePath,
			AdapterType: loc.Type,
		}

		// Discover resources using the adapter
		defs, err := adapter.DiscoverResources(adapterLoc, cp)
		if err != nil {
			return nil, fmt.Errorf("content location %q: failed to discover resources: %w", loc.Name, err)
		}

		allDefinitions = append(allDefinitions, defs...)
	}

	return allDefinitions, nil
}

// DiscoverPromptsWithAdapters discovers prompts using the adapter system
func DiscoverPromptsWithAdapters(locations []domain.ContentLocation, cp *content.ContentProvider, registry *adapters.Registry, configDir string) ([]prompts.PromptDefinition, error) {
	var allDefinitions []prompts.PromptDefinition

	for _, loc := range locations {
		// Get the resolved base path from content provider
		basePath, ok := cp.GetBasePath(loc.Name)
		if !ok {
			return nil, fmt.Errorf("content location %q: base path not found", loc.Name)
		}

		// Resolve adapter for this location
		var adapter adapters.Adapter
		if loc.Type != "" {
			// Explicit adapter type
			var ok bool
			adapter, ok = registry.Get(loc.Type)
			if !ok {
				return nil, fmt.Errorf("content location %q: unknown adapter type %q", loc.Name, loc.Type)
			}
		} else {
			// Auto-detect adapter
			var err error
			adapter, err = registry.AutoDetect(basePath)
			if err != nil {
				return nil, fmt.Errorf("content location %q: %w", loc.Name, err)
			}
		}

		// Create adapter location
		adapterLoc := adapters.Location{
			Name:        loc.Name,
			BasePath:    basePath,
			AdapterType: loc.Type,
		}

		// Discover prompts using the adapter
		defs, err := adapter.DiscoverPrompts(adapterLoc, cp)
		if err != nil {
			return nil, fmt.Errorf("content location %q: failed to discover prompts: %w", loc.Name, err)
		}

		allDefinitions = append(allDefinitions, defs...)
	}

	return allDefinitions, nil
}
