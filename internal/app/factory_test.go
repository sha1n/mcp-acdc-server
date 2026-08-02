package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/stretchr/testify/require"
)

func writeMetadataOnly(t *testing.T, metadata string) string {
	t.Helper()
	contentDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadata), 0o600))
	return contentDir
}

func appTestSettings(contentDir string) *config.Settings {
	return &config.Settings{
		ContentDir: contentDir,
		Scheme:     "acdc",
		Search: config.SearchSettings{
			InMemory:   true,
			ResultMode: config.SearchResultModeReferences,
			MaxResults: 10,
		},
	}
}

type factoryContextKey struct{}

// TestShouldWarnLegacyLayoutIgnored_ConditionMatrix pins the predicate that
// decides whether the missing-manifest-over-legacy-layout diagnostic fires,
// independent of the slog.Warn side effect. Each case documents one branch
// of the three-way condition so a future change to any single branch (e.g.
// inverting defaulted, loosening the source-count check, or dropping the
// IsDir guard) fails here rather than silently mis-warning correctly
// configured zero-config servers.
func TestShouldWarnLegacyLayoutIgnored_ConditionMatrix(t *testing.T) {
	oneSource := []domain.SourceDocument{{URI: "acdc://readme", Name: "readme"}}

	tests := []struct {
		name      string
		defaulted bool
		sources   []domain.SourceDocument
		setupDir  func(t *testing.T, contentDir string)
		wantWarn  bool
	}{
		{
			name:      "defaulted, zero sources, mcp-resources dir exists",
			defaulted: true,
			sources:   nil,
			setupDir: func(t *testing.T, contentDir string) {
				require.NoError(t, os.MkdirAll(filepath.Join(contentDir, "mcp-resources"), 0o755))
			},
			wantWarn: true,
		},
		{
			name:      "not defaulted (explicit manifest), zero sources, mcp-resources dir exists",
			defaulted: false,
			sources:   nil,
			setupDir: func(t *testing.T, contentDir string) {
				require.NoError(t, os.MkdirAll(filepath.Join(contentDir, "mcp-resources"), 0o755))
			},
			wantWarn: false,
		},
		{
			name:      "defaulted, at least one source, mcp-resources dir exists",
			defaulted: true,
			sources:   oneSource,
			setupDir: func(t *testing.T, contentDir string) {
				require.NoError(t, os.MkdirAll(filepath.Join(contentDir, "mcp-resources"), 0o755))
			},
			wantWarn: false,
		},
		{
			name:      "defaulted, zero sources, no mcp-resources at all",
			defaulted: true,
			sources:   nil,
			setupDir: func(t *testing.T, contentDir string) {
				// no mcp-resources directory created
			},
			wantWarn: false,
		},
		{
			name:      "defaulted, zero sources, mcp-resources exists but is a regular file",
			defaulted: true,
			sources:   nil,
			setupDir: func(t *testing.T, contentDir string) {
				require.NoError(t, os.WriteFile(filepath.Join(contentDir, "mcp-resources"), []byte("not a directory"), 0o644))
			},
			wantWarn: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contentDir := t.TempDir()
			tt.setupDir(t, contentDir)
			cp := content.NewContentProvider(contentDir)
			discovery := resources.DiscoveryResult{Sources: tt.sources}

			got := shouldWarnLegacyLayoutIgnored(cp, tt.defaulted, discovery)

			require.Equal(t, tt.wantWarn, got)
		})
	}
}

func TestCreateMCPServer_Success(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	promptsDir := filepath.Join(contentDir, "mcp-prompts")
	_ = os.MkdirAll(resourcesDir, 0755)
	_ = os.MkdirAll(promptsDir, 0755)

	metadataContent := `
server:
  name: test
  version: 1.0
  instructions: inst
tools: []
`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	resFile := filepath.Join(resourcesDir, "res.md")
	_ = os.WriteFile(resFile, []byte("---\nname: res\ndescription: A test resource\n---\ncontent"), 0644)

	promptFile := filepath.Join(promptsDir, "prompt.md")
	_ = os.WriteFile(promptFile, []byte("---\nname: prompt\ndescription: A test prompt\n---\nHello"), 0644)

	settings := &config.Settings{
		ContentDir: contentDir,
		Scheme:     "acdc",
		Search: config.SearchSettings{
			InMemory:   true,
			MaxResults: 10,
		},
	}

	server, cleanup, err := CreateMCPServer(context.Background(), settings, "test")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer cleanup()

	if server == nil {
		t.Fatal("Server is nil")
	}
}

// TestCreateMCPServer_MissingManifestNoDefaultDocsSucceedsLeniently covers the
// zero-config path: with no mcp-metadata.yaml and neither README.md nor docs/,
// the built-in defaults apply and the empty match is tolerated rather than
// failing startup. This supersedes the pre-zero-config behavior where an
// absent manifest was a hard configuration error.
func TestCreateMCPServer_MissingManifestNoDefaultDocsSucceedsLeniently(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	_ = os.MkdirAll(contentDir, 0755)

	settings := &config.Settings{
		ContentDir: contentDir,
		Search: config.SearchSettings{
			InMemory:   true,
			MaxResults: 10,
		},
	}

	server, cleanup, err := CreateMCPServer(context.Background(), settings, "test")
	if err != nil {
		t.Fatalf("Expected success under the zero-config default, got: %v", err)
	}
	defer cleanup()

	if server == nil {
		t.Fatal("Server is nil")
	}
}

// TestCreateMCPServer_MissingManifestWithDefaultDocsSucceeds covers the
// zero-config path where the content root has README.md and docs/**/*.md,
// matching the built-in default include patterns.
func TestCreateMCPServer_MissingManifestWithDefaultDocsSucceeds(t *testing.T) {
	contentDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(contentDir, "README.md"), []byte("# Hello\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(contentDir, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(contentDir, "docs", "a.md"), []byte("# Doc A\n"), 0o644))

	server, cleanup, err := CreateMCPServer(context.Background(), appTestSettings(contentDir), "test")
	require.NoError(t, err)
	defer cleanup()
	require.NotNil(t, server)
}

// TestCreateMCPServer_MissingManifestOverLegacyLayoutSucceeds covers a
// content root laid out for legacy discovery (mcp-resources/**.md) but
// missing mcp-metadata.yaml. Zero-config defaults always set a non-nil Index,
// so discovery never reaches the legacy mcp-resources scan; startup must
// still succeed, and the diagnostic warning this path is expected to emit
// must not itself break server construction. The resulting zero indexed
// source count is verified separately by
// TestZeroConfig_LegacyResourcesLayoutIsNotDiscovered in
// tests/integration/zero_config_test.go.
func TestCreateMCPServer_MissingManifestOverLegacyLayoutSucceeds(t *testing.T) {
	contentDir := t.TempDir()
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	require.NoError(t, os.MkdirAll(resourcesDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(resourcesDir, "guide.md"),
		[]byte("---\nname: Guide\ndescription: A guide.\n---\ncontent"), 0o644))

	server, cleanup, err := CreateMCPServer(context.Background(), appTestSettings(contentDir), "test")
	require.NoError(t, err)
	defer cleanup()
	require.NotNil(t, server)
}

// TestCreateMCPServer_DefaultedMetadataCarriesInjectedVersion verifies the
// build-injected version reaches the metadata used to build the server: with
// no manifest, Server.Version has no other source than the version parameter.
func TestCreateMCPServer_DefaultedMetadataCarriesInjectedVersion(t *testing.T) {
	contentDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(contentDir, "README.md"), []byte("# Hello\n"), 0o644))

	server, cleanup, err := CreateMCPServer(context.Background(), appTestSettings(contentDir), "9.9.9")
	require.NoError(t, err)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	_, err = server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	require.Equal(t, "9.9.9", session.InitializeResult().ServerInfo.Version)
}

func TestCreateMCPServer_InvalidMetadataYAML(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	_ = os.MkdirAll(contentDir, 0755)

	// Write invalid YAML
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte("not: valid: yaml: {{"), 0644)

	settings := &config.Settings{
		ContentDir: contentDir,
		Search: config.SearchSettings{
			InMemory:   true,
			MaxResults: 10,
		},
	}

	_, _, err := CreateMCPServer(context.Background(), settings, "test")
	if err == nil {
		t.Fatal("Expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "failed to parse metadata") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestCreateMCPServer_MetadataValidationFails(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	_ = os.MkdirAll(contentDir, 0755)

	// Empty metadata fails validation
	metadataContent := `
server:
  name: ""
  version: ""
  instructions: ""
`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	settings := &config.Settings{
		ContentDir: contentDir,
		Search: config.SearchSettings{
			InMemory:   true,
			MaxResults: 10,
		},
	}

	_, _, err := CreateMCPServer(context.Background(), settings, "test")
	if err == nil {
		t.Fatal("Expected error for invalid metadata")
	}
	if !strings.Contains(err.Error(), "metadata validation failed") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestCreateMCPServer_InvalidResourceIsSkipped(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	_ = os.MkdirAll(resourcesDir, 0755)

	metadataContent := `
server:
  name: test
  version: 1.0
  instructions: inst
tools: []
`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	// Write an invalid resource file (invalid frontmatter) - should be skipped with warning
	_ = os.WriteFile(filepath.Join(resourcesDir, "invalid.md"), []byte("---\n: broken\n---\ncontent"), 0644)

	settings := &config.Settings{
		ContentDir: contentDir,
		Scheme:     "acdc",
		Search: config.SearchSettings{
			InMemory:   true,
			MaxResults: 10,
		},
	}

	// Invalid resources are skipped, not failed
	server, cleanup, err := CreateMCPServer(context.Background(), settings, "test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if server == nil {
		t.Fatal("Server is nil")
	}
}

func TestCreateMCPServer_ResourceWithKeywords(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	_ = os.MkdirAll(resourcesDir, 0755)

	metadataContent := `server: { name: test, version: 1.0, instructions: inst }`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	// Resource with keywords
	resFile := filepath.Join(resourcesDir, "res.md")
	_ = os.WriteFile(resFile, []byte("---\nname: res\ndescription: desc\nkeywords: k1,k2\n---\ncontent"), 0644)

	settings := &config.Settings{
		ContentDir: contentDir,
		Scheme:     "acdc",
		Search:     config.SearchSettings{InMemory: true, MaxResults: 10},
	}

	server, cleanup, err := CreateMCPServer(context.Background(), settings, "test")
	if err != nil {
		t.Fatalf("Failed: %v", err)
	}
	defer cleanup()

	if server == nil {
		t.Fatal("Server is nil")
	}
}

func TestCreateMCPServer_NoResources(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	_ = os.MkdirAll(resourcesDir, 0755)

	metadataContent := `
server:
  name: test
  version: 1.0
  instructions: inst
tools: []
`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	settings := &config.Settings{
		ContentDir: contentDir,
		Scheme:     "acdc",
		Search: config.SearchSettings{
			InMemory:   true,
			MaxResults: 10,
		},
	}

	// Should succeed with no resources
	server, cleanup, err := CreateMCPServer(context.Background(), settings, "test")
	if err != nil {
		t.Fatalf("Failed to create server with no resources: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if server == nil {
		t.Fatal("Server is nil")
	}
}

func TestCreateMCPServer_ConfiguredDiscoveryFailure(t *testing.T) {
	contentDir := writeMetadataOnly(t, `server:
  name: test
  version: "1"
  instructions: test
index:
  include: ["docs/**/*.md"]
`)

	_, cleanup, err := CreateMCPServer(context.Background(), appTestSettings(contentDir), "test")

	require.ErrorContains(t, err, "configured index matched no files")
	require.Nil(t, cleanup)
}

func TestCreateMCPServer_IndexFailureStopsConstruction(t *testing.T) {
	contentDir := writeMetadataOnly(t, `server:
  name: test
  version: "1"
  instructions: test
`)
	indexer := &fakeSearcher{indexErr: errors.New("index failed")}
	deps := defaultFactoryDeps()
	deps.discover = func(context.Context, *content.ContentProvider, *domain.IndexMetadata, string, ...resources.DiscoverOption) (resources.DiscoveryResult, error) {
		return resources.DiscoveryResult{}, nil
	}
	deps.newSearch = func(config.SearchSettings) search.Searcher {
		return indexer
	}

	server, cleanup, err := createMCPServer(context.Background(), appTestSettings(contentDir), "test", deps)

	require.ErrorContains(t, err, "index chunks: index failed")
	require.Nil(t, server)
	require.Nil(t, cleanup)
	require.Equal(t, 1, indexer.closeCalls)
}

func TestCreateMCPServer_PassesCallerContextToDiscovery(t *testing.T) {
	contentDir := writeMetadataOnly(t, `server:
  name: test
  version: "1"
  instructions: test
`)
	ctx := context.WithValue(context.Background(), factoryContextKey{}, "request-context")
	deps := defaultFactoryDeps()
	var discoveryCtx context.Context
	deps.discover = func(gotCtx context.Context, _ *content.ContentProvider, _ *domain.IndexMetadata, _ string, _ ...resources.DiscoverOption) (resources.DiscoveryResult, error) {
		discoveryCtx = gotCtx
		return resources.DiscoveryResult{}, nil
	}
	deps.newSearch = func(config.SearchSettings) search.Searcher {
		return &fakeSearcher{drain: true}
	}

	server, cleanup, err := createMCPServer(ctx, appTestSettings(contentDir), "test", deps)

	require.NoError(t, err)
	require.NotNil(t, server)
	require.NotNil(t, cleanup)
	require.Same(t, ctx, discoveryCtx)
	cleanup()
}

// TestCreateMCPServer_FingerprintFailureAtStartupDoesNotStopConstruction pins
// the deliberate startup design: a fingerprint failure only warns and seeds an
// empty walk digest, it never fails construction. The manifest's include
// pattern is invalid, which fails the real resources.Fingerprint call inside
// createMCPServer; deps.discover is injected to succeed regardless of that
// same metadata, isolating the fingerprint failure from the discovery failure
// that identical invalid metadata would otherwise also produce.
func TestCreateMCPServer_FingerprintFailureAtStartupDoesNotStopConstruction(t *testing.T) {
	contentDir := writeMetadataOnly(t, `server:
  name: test
  version: "1"
  instructions: test
index:
  include: ["/abs/pattern/**"]
`)
	deps := defaultFactoryDeps()
	deps.discover = func(context.Context, *content.ContentProvider, *domain.IndexMetadata, string, ...resources.DiscoverOption) (resources.DiscoveryResult, error) {
		return resources.DiscoveryResult{}, nil
	}
	deps.newSearch = func(config.SearchSettings) search.Searcher {
		return &fakeSearcher{drain: true}
	}

	server, cleanup, err := createMCPServer(context.Background(), appTestSettings(contentDir), "test", deps)

	require.NoError(t, err)
	require.NotNil(t, server)
	require.NotNil(t, cleanup)
	cleanup()
}

func TestCreateMCPServer_FailsWhenConfiguredDiscoveryFails(t *testing.T) {
	contentDir := t.TempDir()
	metadata := `
server: { name: test, version: 1.0, instructions: inst }
tools: []
index:
  include: ["docs/**"]
`
	require.NoError(t, os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadata), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(contentDir, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(contentDir, "docs", "data.txt"), []byte("not markdown"), 0o644))

	_, _, err := CreateMCPServer(context.Background(), &config.Settings{ContentDir: contentDir, Scheme: "acdc", Search: config.SearchSettings{InMemory: true}}, "test")
	require.ErrorContains(t, err, "failed to discover resources")
	require.ErrorContains(t, err, "configured index selected non-Markdown file")
}

func TestCreateMCPServer_FailsWhenDiscoveredSourcesShareURI(t *testing.T) {
	contentDir := t.TempDir()
	metadata := `
server: { name: test, version: 1.0, instructions: inst }
tools: []
index:
  include: ["docs/*"]
`
	require.NoError(t, os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadata), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(contentDir, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(contentDir, "docs", "guide.md"), []byte("# Guide"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(contentDir, "docs", "guide.markdown"), []byte("# Guide duplicate"), 0o644))

	_, _, err := CreateMCPServer(context.Background(), &config.Settings{ContentDir: contentDir, Scheme: "acdc", Search: config.SearchSettings{InMemory: true}}, "test")
	require.ErrorContains(t, err, "failed to create resource provider")
	require.ErrorContains(t, err, "duplicate source URI: acdc://docs/guide")
}

func TestCreateMCPServer_InvalidToolMetadata_MissingName(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	_ = os.MkdirAll(contentDir, 0755)

	metadataContent := `
server: { name: test, version: 1.0, instructions: inst }
tools:
  - name: ""
    description: "desc"
`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	settings := &config.Settings{ContentDir: contentDir}
	_, _, err := CreateMCPServer(context.Background(), settings, "test")
	if err == nil || !strings.Contains(err.Error(), "metadata validation failed") {
		t.Errorf("Expected metadata validation error, got: %v", err)
	}
}

func TestCreateMCPServer_InvalidToolMetadata_MissingDescription(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	_ = os.MkdirAll(contentDir, 0755)

	metadataContent := `
server: { name: test, version: 1.0, instructions: inst }
tools:
  - name: "search"
    description: ""
`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	settings := &config.Settings{ContentDir: contentDir}
	_, _, err := CreateMCPServer(context.Background(), settings, "test")
	if err == nil || !strings.Contains(err.Error(), "metadata validation failed") {
		t.Errorf("Expected metadata validation error, got: %v", err)
	}
}

func TestCreateMCPServer_InvalidToolMetadata_DuplicateNames(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	_ = os.MkdirAll(contentDir, 0755)

	metadataContent := `
server: { name: test, version: 1.0, instructions: inst }
tools:
  - { name: search, description: d1 }
  - { name: search, description: d2 }
`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	settings := &config.Settings{ContentDir: contentDir}
	_, _, err := CreateMCPServer(context.Background(), settings, "test")
	if err == nil || !strings.Contains(err.Error(), "duplicate tool name") {
		t.Errorf("Expected duplicate tool name error, got: %v", err)
	}
}
func TestCreateMCPServer_PromptDiscoveryError(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	_ = os.MkdirAll(contentDir, 0755)

	metadataContent := `server: { name: test, version: 1.0, instructions: inst }`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	// Create resources dir so it doesn't fail here
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	_ = os.MkdirAll(resourcesDir, 0755)

	// Create a symlink loop to cause os.Stat to fail with "too many levels of symbolic links"
	promptsDir := filepath.Join(contentDir, "mcp-prompts")
	_ = os.Symlink(promptsDir, promptsDir)

	settings := &config.Settings{
		ContentDir: contentDir,
		Scheme:     "acdc",
		Search:     config.SearchSettings{InMemory: true},
	}

	_, _, err := CreateMCPServer(context.Background(), settings, "test")
	if err == nil {
		t.Fatal("Expected error for prompt discovery failure")
	}
	if !strings.Contains(err.Error(), "failed to discover prompts") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestCreateMCPServer_CrossRefTransformation(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	guidesDir := filepath.Join(resourcesDir, "guides")
	_ = os.MkdirAll(guidesDir, 0755)
	_ = os.MkdirAll(filepath.Join(contentDir, "mcp-prompts"), 0755)

	metadataContent := `server: { name: test, version: 1.0, instructions: inst }`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	// Resource A links to Resource B via relative markdown link
	resA := filepath.Join(resourcesDir, "doc-a.md")
	_ = os.WriteFile(resA, []byte("---\nname: Doc A\ndescription: Document A\n---\nSee [Doc B](guides/doc-b.md) for more."), 0644)

	// Resource B links back to A via parent-relative link
	resB := filepath.Join(guidesDir, "doc-b.md")
	_ = os.WriteFile(resB, []byte("---\nname: Doc B\ndescription: Document B\n---\nBack to [Doc A](../doc-a.md)."), 0644)

	settings := &config.Settings{
		ContentDir: contentDir,
		Scheme:     "acdc",
		CrossRef:   true,
		Search:     config.SearchSettings{InMemory: true, MaxResults: 10},
	}

	server, cleanup, err := CreateMCPServer(context.Background(), settings, "test")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer cleanup()

	if server == nil {
		t.Fatal("Server is nil")
	}
}

func TestCreateMCPServer_CrossRefTransformation_ContentVerification(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	guidesDir := filepath.Join(resourcesDir, "guides")
	_ = os.MkdirAll(guidesDir, 0755)

	// Resource A links to Resource B via relative markdown link
	resA := filepath.Join(resourcesDir, "doc-a.md")
	_ = os.WriteFile(resA, []byte("---\nname: Doc A\ndescription: Document A\n---\nSee [Doc B](guides/doc-b.md) for more."), 0644)

	// Resource B links back to A via parent-relative link
	resB := filepath.Join(guidesDir, "doc-b.md")
	_ = os.WriteFile(resB, []byte("---\nname: Doc B\ndescription: Document B\n---\nBack to [Doc A](../doc-a.md)."), 0644)

	// Replicate the factory wiring to verify content transformation
	cp := content.NewContentProvider(contentDir)
	defs, err := resources.DiscoverResources(cp, "acdc")
	if err != nil {
		t.Fatalf("DiscoverResources error: %v", err)
	}

	transformer := resources.NewCrossRefTransformer(defs, "acdc")
	provider, err := resources.NewResourceProvider(defs, nil, resources.WithTransformer(transformer))
	if err != nil {
		t.Fatalf("NewResourceProvider error: %v", err)
	}

	// Read Doc A - should have transformed link to Doc B
	contentA, err := provider.ReadResource("acdc://doc-a")
	if err != nil {
		t.Fatalf("ReadResource doc-a error: %v", err)
	}
	if !strings.Contains(contentA, "acdc://guides/doc-b") {
		t.Errorf("Doc A content should contain transformed URI 'acdc://guides/doc-b', got: %s", contentA)
	}
	if strings.Contains(contentA, "guides/doc-b.md") {
		t.Errorf("Doc A content should NOT contain original relative path 'guides/doc-b.md', got: %s", contentA)
	}

	// Read Doc B - should have transformed link back to Doc A
	contentB, err := provider.ReadResource("acdc://guides/doc-b")
	if err != nil {
		t.Fatalf("ReadResource doc-b error: %v", err)
	}
	if !strings.Contains(contentB, "acdc://doc-a") {
		t.Errorf("Doc B content should contain transformed URI 'acdc://doc-a', got: %s", contentB)
	}
	if strings.Contains(contentB, "../doc-a.md") {
		t.Errorf("Doc B content should NOT contain original relative path '../doc-a.md', got: %s", contentB)
	}
}

func TestCreateMCPServer_CrossRefDisabledByDefault(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	guidesDir := filepath.Join(resourcesDir, "guides")
	_ = os.MkdirAll(guidesDir, 0755)

	// Resource A links to Resource B via relative markdown link
	resA := filepath.Join(resourcesDir, "doc-a.md")
	_ = os.WriteFile(resA, []byte("---\nname: Doc A\ndescription: Document A\n---\nSee [Doc B](guides/doc-b.md) for more."), 0644)

	resB := filepath.Join(guidesDir, "doc-b.md")
	_ = os.WriteFile(resB, []byte("---\nname: Doc B\ndescription: Document B\n---\nBack to [Doc A](../doc-a.md)."), 0644)

	// CrossRef not set (defaults to false)
	cp := content.NewContentProvider(contentDir)
	defs, err := resources.DiscoverResources(cp, "acdc")
	if err != nil {
		t.Fatalf("DiscoverResources error: %v", err)
	}

	provider, err := resources.NewResourceProvider(defs, nil)
	if err != nil {
		t.Fatalf("NewResourceProvider error: %v", err)
	}

	contentA, err := provider.ReadResource("acdc://doc-a")
	if err != nil {
		t.Fatalf("ReadResource doc-a error: %v", err)
	}
	if !strings.Contains(contentA, "guides/doc-b.md") {
		t.Errorf("With cross-ref disabled, Doc A should retain original relative link 'guides/doc-b.md', got: %s", contentA)
	}

	contentB, err := provider.ReadResource("acdc://guides/doc-b")
	if err != nil {
		t.Fatalf("ReadResource doc-b error: %v", err)
	}
	if !strings.Contains(contentB, "../doc-a.md") {
		t.Errorf("With cross-ref disabled, Doc B should retain original relative link '../doc-a.md', got: %s", contentB)
	}
}

func TestCreateMCPServer_CrossRefTransformation_CustomScheme(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	_ = os.MkdirAll(resourcesDir, 0755)

	resA := filepath.Join(resourcesDir, "a.md")
	_ = os.WriteFile(resA, []byte("---\nname: A\ndescription: A\n---\nSee [B](b.md)."), 0644)

	resB := filepath.Join(resourcesDir, "b.md")
	_ = os.WriteFile(resB, []byte("---\nname: B\ndescription: B\n---\nContent B."), 0644)

	cp := content.NewContentProvider(contentDir)
	defs, err := resources.DiscoverResources(cp, "myco")
	if err != nil {
		t.Fatalf("DiscoverResources error: %v", err)
	}

	transformer := resources.NewCrossRefTransformer(defs, "myco")
	provider, err := resources.NewResourceProvider(defs, nil, resources.WithTransformer(transformer))
	if err != nil {
		t.Fatalf("NewResourceProvider error: %v", err)
	}

	contentA, err := provider.ReadResource("myco://a")
	if err != nil {
		t.Fatalf("ReadResource error: %v", err)
	}
	if !strings.Contains(contentA, "myco://b") {
		t.Errorf("Content should contain 'myco://b', got: %s", contentA)
	}
}
