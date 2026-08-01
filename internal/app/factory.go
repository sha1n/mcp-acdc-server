package app

import (
	"context"
	"fmt"
	"os"

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

type factoryDeps struct {
	discover  func(context.Context, *content.ContentProvider, *domain.IndexMetadata, string) (resources.DiscoveryResult, error)
	newSearch func(config.SearchSettings) search.Searcher
}

func defaultFactoryDeps() factoryDeps {
	return factoryDeps{
		discover: resources.Discover,
		newSearch: func(settings config.SearchSettings) search.Searcher {
			return search.NewService(settings)
		},
	}
}

// CreateMCPServer initializes the core MCP server components.
func CreateMCPServer(ctx context.Context, settings *config.Settings) (*mcpsdk.Server, func(), error) {
	return createMCPServer(ctx, settings, defaultFactoryDeps())
}

func createMCPServer(ctx context.Context, settings *config.Settings, deps factoryDeps) (*mcpsdk.Server, func(), error) {
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
	discovery, err := deps.discover(ctx, cp, metadata.Index, settings.Scheme)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to discover resources: %w", err)
	}

	var resourceOpts []resources.Option
	if settings.CrossRef {
		resourceOpts = append(resourceOpts, resources.WithTransformer(
			resources.NewCrossRefTransformer(discovery.Sources, settings.Scheme),
		))
	}
	resourceProvider, err := resources.NewResourceProvider(discovery.Sources, discovery.Chunks, resourceOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create resource provider: %w", err)
	}

	// Discover prompts
	promptDefinitions, err := prompts.DiscoverPrompts(cp)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to discover prompts: %w", err)
	}

	promptProvider := prompts.NewPromptProvider(promptDefinitions, cp)

	// Initialize search service
	searchService := deps.newSearch(settings.Search)

	// Index resources
	if err := IndexResources(ctx, resourceProvider, searchService); err != nil {
		searchService.Close()
		return nil, nil, err
	}

	// Create MCP server
	mcpServer := mcp.CreateServer(metadata, resourceProvider, promptProvider, searchService, settings.Search)

	return mcpServer, searchService.Close, nil
}
