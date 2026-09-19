package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/embed"
	"github.com/sha1n/mcp-acdc-server/internal/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/prompts"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/search"
)

type factoryDeps struct {
	discover    func(context.Context, *content.ContentProvider, *domain.IndexMetadata, string, ...resources.DiscoverOption) (resources.DiscoveryResult, error)
	newSearch   func(config.SearchSettings) search.Searcher
	newEmbedder func(modelPath string) (embed.Embedder, error)
}

func defaultFactoryDeps() factoryDeps {
	return factoryDeps{
		discover: resources.Discover,
		newSearch: func(settings config.SearchSettings) search.Searcher {
			return search.NewService(settings)
		},
		// No embedding backend is supplied by default, and this package
		// deliberately links none: an adapter is the caller's to choose.
		// Reporting its absence is the honest behaviour, because a
		// configured model path that cannot be honoured is a broken
		// deployment, and running silently without semantic search would
		// hide it.
		newEmbedder: func(string) (embed.Embedder, error) {
			return nil, embed.ErrNoBackend
		},
	}
}

// Option supplies an implementation the factory cannot choose for itself.
type Option func(*factoryDeps)

// WithEmbedderProvider supplies the embedding backend opened when an operator
// configures a semantic model; the provider receives the configured model
// path. Callers that supply none get ErrNoBackend for a configured path,
// since no adapter is linked into this package.
//
// A nil provider is ignored rather than installed, so a caller that passes
// one still gets that honest ErrNoBackend instead of a nil call at startup.
func WithEmbedderProvider(provider func(modelPath string) (embed.Embedder, error)) Option {
	return func(deps *factoryDeps) {
		if provider != nil {
			deps.newEmbedder = provider
		}
	}
}

// CreateMCPServer initializes the core MCP server components.
func CreateMCPServer(ctx context.Context, settings *config.Settings, version string, opts ...Option) (*mcpsdk.Server, func(), error) {
	deps := defaultFactoryDeps()
	for _, opt := range opts {
		opt(&deps)
	}
	return createMCPServer(ctx, settings, version, deps)
}

func createMCPServer(ctx context.Context, settings *config.Settings, version string, deps factoryDeps) (*mcpsdk.Server, func(), error) {
	// Initialize content provider
	cp := content.NewContentProvider(settings.ContentDir)

	if err := rejectLegacyContentLayout(cp); err != nil {
		return nil, nil, err
	}

	resolved, err := resolveMetadata(cp, version)
	if err != nil {
		return nil, nil, err
	}
	metadata := resolved.metadata

	var discoverOpts []resources.DiscoverOption
	if resolved.defaulted {
		discoverOpts = append(discoverOpts, resources.WithLenientIndex())
	}

	// Fingerprint before discovery so a file that changes between the two
	// calls is seen as pending work by the first refresh check rather than
	// silently folded into the seed digest. A fingerprint failure here is not
	// fatal: a server that indexes fine must not refuse to start because a
	// refresh gate could not be primed, so an empty digest is seeded instead
	// and the first Revalidate call treats the content root as changed.
	seedWalkDigest, err := resources.Fingerprint(ctx, cp, metadata.Index, discoverOpts...)
	if err != nil {
		slog.Warn("failed to fingerprint content root at startup; catalog refresh will re-discover on first check",
			"error", err)
		seedWalkDigest = ""
	}

	// Discover resources
	discovery, err := deps.discover(ctx, cp, metadata.Index, settings.Scheme, discoverOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to discover resources: %w", err)
	}

	warnIfLegacyLayoutIgnored(cp, resolved.defaulted, discovery)

	resourceProvider, err := assembleResourceProvider(discovery, settings.CrossRef, settings.Scheme)
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
	searchService, err := newSearchService(settings.Search, deps)
	if err != nil {
		return nil, nil, err
	}

	// Index resources
	if err := IndexResources(ctx, resourceProvider, searchService); err != nil {
		searchService.Close()
		return nil, nil, err
	}

	holder := resources.NewCatalogHolder(resourceProvider)

	// The refresher closes over the metadata and discover options resolved
	// once above, so a mid-session refresh applies the same zero-config
	// leniency decision as startup rather than re-deciding it.
	refresher := &catalogRefresher{
		holder:   holder,
		searcher: searchService,
		fingerprint: func(ctx context.Context) (string, error) {
			return resources.Fingerprint(ctx, cp, metadata.Index, discoverOpts...)
		},
		discover: func(ctx context.Context) (resources.DiscoveryResult, error) {
			return deps.discover(ctx, cp, metadata.Index, settings.Scheme, discoverOpts...)
		},
		assemble: func(discovery resources.DiscoveryResult) (*resources.ResourceProvider, error) {
			return assembleResourceProvider(discovery, settings.CrossRef, settings.Scheme)
		},
		now:           time.Now,
		interval:      refreshInterval,
		walkDigest:    seedWalkDigest,
		contentDigest: computeContentDigest(discovery.Sources),
	}
	refresher.reindex = func(ctx context.Context, provider *resources.ResourceProvider) error {
		return IndexResources(ctx, provider, refresher.searcher)
	}

	// Create MCP server. mcp.CreateServer needs the refresher to build the
	// registrar it returns, so the registrar is bound onto the refresher only
	// after this call — the narrow window Revalidate's nil-registrar guard
	// covers.
	mcpServer, registrar := mcp.CreateServer(metadata, holder, promptProvider, searchService, settings.Search, refresher)
	refresher.bindRegistrar(registrar)

	return mcpServer, searchService.Close, nil
}

// newSearchService builds the lexical retriever and, when an operator has
// configured a model, decorates it with semantic retrieval. This is the whole
// of the semantic wiring: everything downstream speaks only to search.Searcher.
func newSearchService(settings config.SearchSettings, deps factoryDeps) (search.Searcher, error) {
	lexical := deps.newSearch(settings)
	if settings.SemanticModel == "" {
		slog.Info("Semantic search disabled, serving lexical results only")
		return lexical, nil
	}

	embedder, err := deps.newEmbedder(settings.SemanticModel)
	if err != nil {
		lexical.Close()
		return nil, fmt.Errorf("failed to load semantic model %q: %w", settings.SemanticModel, err)
	}

	hybrid, err := search.NewHybridService(lexical, embedder, settings)
	if err != nil {
		lexical.Close()
		return nil, fmt.Errorf("failed to enable semantic search for model %q: %w", settings.SemanticModel, err)
	}

	// The model geometry is reported by the loaded embedder, not by the
	// configuration, so this line is the only place an operator can confirm
	// which model actually answered.
	info := embedder.Info()
	slog.Info("Semantic search enabled",
		"model", settings.SemanticModel,
		"dimensions", info.Dimensions,
		"max_tokens", info.MaxTokens,
		"floor", settings.SemanticFloor)

	return hybrid, nil
}

// assembleResourceProvider builds the resource catalog from a discovery
// result exactly the same way at startup and on every later refresh,
// cross-ref transformer included, so a future change to provider options
// cannot apply to only one of those call sites.
func assembleResourceProvider(discovery resources.DiscoveryResult, crossRef bool, scheme string) (*resources.ResourceProvider, error) {
	var opts []resources.Option
	if crossRef {
		opts = append(opts, resources.WithTransformer(
			resources.NewCrossRefTransformer(discovery.Sources, scheme),
		))
	}
	return resources.NewResourceProvider(discovery.Sources, discovery.Chunks, opts...)
}

// warnIfLegacyLayoutIgnored flags the likely cause of a silently empty
// zero-config server: a manifest-less content root laid out for legacy
// .acdc/resources discovery. Zero-config defaults always set a non-nil Index,
// which routes discovery to the configured-index path (README.md and
// docs/**) rather than the legacy .acdc/resources scan, so a .acdc/resources/
// directory goes unindexed without any error. This is diagnostic only; it
// does not change which discovery mode is selected or whether startup
// succeeds.
func warnIfLegacyLayoutIgnored(cp *content.ContentProvider, defaulted bool, discovery resources.DiscoveryResult) {
	if !shouldWarnLegacyLayoutIgnored(cp, defaulted, discovery) {
		return
	}
	slog.Warn("Found .acdc/resources/ directory but no .acdc/config.yaml manifest; "+
		"zero-config mode indexes only README.md and docs/**, so .acdc/resources/ is not indexed",
		"resources_dir", cp.ResourcesDir,
		"manifest_path", cp.ConfigFile)
}

// shouldWarnLegacyLayoutIgnored reports whether defaulted metadata produced
// zero discovered sources while a .acdc/resources/ directory sits unindexed
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
