package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/prompts"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/search"
)

type factoryDeps struct {
	discover  func(context.Context, *content.ContentProvider, *domain.IndexMetadata, string, ...resources.DiscoverOption) (resources.DiscoveryResult, error)
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
func CreateMCPServer(ctx context.Context, settings *config.Settings, version string) (*mcpsdk.Server, func(), error) {
	return createMCPServer(ctx, settings, version, defaultFactoryDeps())
}

func createMCPServer(ctx context.Context, settings *config.Settings, version string, deps factoryDeps) (*mcpsdk.Server, func(), error) {
	// Initialize content provider
	cp := content.NewContentProvider(settings.ContentDir)

	resolved, err := resolveMetadata(cp, version)
	if err != nil {
		return nil, nil, err
	}
	metadata := resolved.metadata

	var discoverOpts []resources.DiscoverOption
	if resolved.defaulted {
		discoverOpts = append(discoverOpts, resources.WithLenientIndex())
	}

	// Discover resources
	discovery, err := deps.discover(ctx, cp, metadata.Index, settings.Scheme, discoverOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to discover resources: %w", err)
	}

	warnIfLegacyLayoutIgnored(cp, resolved.defaulted, discovery)

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

// warnIfLegacyLayoutIgnored flags the likely cause of a silently empty
// zero-config server: a manifest-less content root laid out for legacy
// mcp-resources discovery. Zero-config defaults always set a non-nil Index,
// which routes discovery to the configured-index path (README.md and
// docs/**) rather than the legacy mcp-resources scan, so an mcp-resources/
// directory goes unindexed without any error. This is diagnostic only; it
// does not change which discovery mode is selected or whether startup
// succeeds.
func warnIfLegacyLayoutIgnored(cp *content.ContentProvider, defaulted bool, discovery resources.DiscoveryResult) {
	if !shouldWarnLegacyLayoutIgnored(cp, defaulted, discovery) {
		return
	}
	slog.Warn("Found mcp-resources/ directory but no mcp-metadata.yaml manifest; "+
		"zero-config mode indexes only README.md and docs/**, so mcp-resources/ is not indexed",
		"resources_dir", cp.ResourcesDir,
		"manifest_path", cp.GetPath(metadataFileName))
}

// shouldWarnLegacyLayoutIgnored reports whether defaulted metadata produced
// zero discovered sources while an mcp-resources/ directory sits unindexed
// at the content root.
func shouldWarnLegacyLayoutIgnored(cp *content.ContentProvider, defaulted bool, discovery resources.DiscoveryResult) bool {
	if !defaulted || len(discovery.Sources) > 0 {
		return false
	}
	info, err := os.Stat(cp.ResourcesDir)
	if err != nil || !info.IsDir() {
		return false
	}
	return true
}
