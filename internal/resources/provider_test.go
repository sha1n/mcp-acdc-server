package resources

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestResourceProvider_ReadSourceAndChunks(t *testing.T) {
	source := domain.SourceDocument{ID: "acdc://guide", URI: "acdc://guide", Name: "Guide", Description: "Guide", MIMEType: "text/markdown", Content: "# Setup\n\nA.\n\nB.\n"}
	chunks := []domain.Chunk{
		{ID: "acdc://guide#setup~1", SourceID: source.ID, SourceURI: source.URI, ChunkURI: "acdc://guide#setup~1", SectionFragment: "setup", Fragment: "setup~1", Part: 1, PartCount: 2, Content: "# Setup\n\nA."},
		{ID: "acdc://guide#setup~2", SourceID: source.ID, SourceURI: source.URI, ChunkURI: "acdc://guide#setup~2", SectionFragment: "setup", Fragment: "setup~2", Part: 2, PartCount: 2, Content: "B."},
	}
	provider, err := NewResourceProvider([]domain.SourceDocument{source}, chunks)
	require.NoError(t, err)

	body, err := provider.ReadResource("acdc://guide")
	require.NoError(t, err)
	require.Equal(t, source.Content, body)
	part, err := provider.ReadResource("acdc://guide#setup~2")
	require.NoError(t, err)
	require.Equal(t, "B.", part)
	section, err := provider.ReadResource("acdc://guide#setup")
	require.NoError(t, err)
	require.Equal(t, "# Setup\n\nA.\n\nB.", section)
}

func TestResourceProvider_ReadErrors(t *testing.T) {
	provider, err := NewResourceProvider([]domain.SourceDocument{{ID: "acdc://guide", URI: "acdc://guide"}}, nil)
	require.NoError(t, err)
	_, err = provider.ReadResource("acdc://missing")
	require.ErrorIs(t, err, ErrUnknownResource)
	_, err = provider.ReadResource("acdc://guide#missing")
	require.ErrorIs(t, err, ErrUnknownFragment)
}

func TestResourceProvider_DuplicateIdentities(t *testing.T) {
	source := domain.SourceDocument{ID: "acdc://guide", URI: "acdc://guide"}
	chunk := domain.Chunk{ID: "acdc://guide#one", SourceURI: source.URI, ChunkURI: "acdc://guide#one"}

	_, err := NewResourceProvider([]domain.SourceDocument{source, source}, nil)
	require.ErrorContains(t, err, "duplicate source URI")
	_, err = NewResourceProvider([]domain.SourceDocument{source}, []domain.Chunk{chunk, chunk})
	require.ErrorContains(t, err, "duplicate chunk ID")
	_, err = NewResourceProvider([]domain.SourceDocument{source}, []domain.Chunk{chunk, {ID: "other", ChunkURI: chunk.ChunkURI}})
	require.ErrorContains(t, err, "duplicate chunk URI")
}

func TestResourceProvider_StreamChunks_TransformsAndCancels(t *testing.T) {
	source := domain.SourceDocument{ID: "acdc://guide", URI: "acdc://guide", Name: "Guide"}
	chunk := domain.Chunk{ID: "acdc://guide#document", SourceID: source.ID, SourceURI: source.URI, ChunkURI: "acdc://guide#document", Content: "body"}
	provider, err := NewResourceProvider([]domain.SourceDocument{source}, []domain.Chunk{chunk}, WithTransformer(func(body string, _ ResourceDefinition) string {
		return strings.ToUpper(body)
	}))
	require.NoError(t, err)

	stream := make(chan domain.Chunk, 1)
	require.NoError(t, provider.StreamChunks(context.Background(), stream))
	require.Equal(t, "BODY", (<-stream).Content)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, provider.StreamChunks(ctx, make(chan domain.Chunk)), context.Canceled)
}

func TestResourceProvider_TransformerAppliesToEveryReadForm(t *testing.T) {
	source := domain.SourceDocument{ID: "acdc://guide", URI: "acdc://guide", Name: "Guide", Content: "source"}
	chunks := []domain.Chunk{
		{ID: "acdc://guide#part~1", SourceURI: source.URI, ChunkURI: "acdc://guide#part~1", SectionFragment: "part", Content: "first"},
		{ID: "acdc://guide#part~2", SourceURI: source.URI, ChunkURI: "acdc://guide#part~2", SectionFragment: "part", Content: "second"},
	}
	provider, err := NewResourceProvider([]domain.SourceDocument{source}, chunks, WithTransformer(func(body string, definition ResourceDefinition) string {
		return definition.Name + ":" + strings.ToUpper(body)
	}))
	require.NoError(t, err)

	for uri, want := range map[string]string{
		"acdc://guide":        "Guide:SOURCE",
		"acdc://guide#part~1": "Guide:FIRST",
		"acdc://guide#part":   "Guide:FIRST\n\nSECOND",
	} {
		got, err := provider.ReadResource(uri)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

func TestResourceProvider_StreamChunks_StopsWhileBlocked(t *testing.T) {
	source := domain.SourceDocument{ID: "acdc://guide", URI: "acdc://guide"}
	chunk := domain.Chunk{ID: "acdc://guide#document", SourceURI: source.URI, ChunkURI: "acdc://guide#document"}
	provider, err := NewResourceProvider([]domain.SourceDocument{source}, []domain.Chunk{chunk})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errs := make(chan error, 1)
	go func() { errs <- provider.StreamChunks(ctx, make(chan domain.Chunk)) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	require.ErrorIs(t, <-errs, context.Canceled)
}

func TestResourceProvider_ListsAndStreamsImmutableSources(t *testing.T) {
	source := domain.SourceDocument{
		ID: "acdc://guide", URI: "acdc://guide", Name: "Guide", Description: "Read this", MIMEType: "text/markdown",
		Content: "body", Keywords: []string{"guide"},
	}
	provider, err := NewResourceProvider([]domain.SourceDocument{source}, nil, WithTransformer(func(body string, _ ResourceDefinition) string {
		return strings.ToUpper(body)
	}))
	require.NoError(t, err)

	// Mutating the caller-owned source must not change the catalog exposed to MCP or indexing.
	source.Name = "Changed"
	source.Keywords[0] = "changed"
	listed := provider.ListResources()
	require.Equal(t, []string{"acdc://guide"}, []string{listed[0].URI})
	require.Equal(t, "Guide", listed[0].Name)
	require.Equal(t, "Read this", listed[0].Description)
	require.Equal(t, "text/markdown", listed[0].MIMEType)

	stream := make(chan domain.Document, 1)
	require.NoError(t, provider.StreamResources(context.Background(), stream))
	document := <-stream
	require.Equal(t, "acdc://guide", document.URI)
	require.Equal(t, "Guide", document.Name)
	require.Equal(t, "BODY", document.Content)
	require.Equal(t, []string{"guide"}, document.Keywords)
}

func TestResourceProvider_StreamResourcesHonorsCancellation(t *testing.T) {
	provider, err := NewResourceProvider([]domain.SourceDocument{{ID: "acdc://guide", URI: "acdc://guide"}}, nil)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, provider.StreamResources(ctx, make(chan domain.Document)), context.Canceled)
}

func TestResourceProvider_StreamResourcesStopsWhileBlocked(t *testing.T) {
	provider, err := NewResourceProvider([]domain.SourceDocument{{ID: "acdc://guide", URI: "acdc://guide", Content: "body"}}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() { errs <- provider.StreamResources(ctx, make(chan domain.Document)) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	require.ErrorIs(t, <-errs, context.Canceled)
}

func TestResourceProvider_StreamsOrphanChunkWithSourceIdentity(t *testing.T) {
	chunk := domain.Chunk{ID: "acdc://orphan#document", SourceID: "acdc://orphan", SourceURI: "acdc://orphan", ChunkURI: "acdc://orphan#document", Content: "body"}
	provider, err := NewResourceProvider(nil, []domain.Chunk{chunk}, WithTransformer(func(body string, source ResourceDefinition) string {
		return source.ID + ":" + body
	}))
	require.NoError(t, err)

	stream := make(chan domain.Chunk, 1)
	require.NoError(t, provider.StreamChunks(context.Background(), stream))
	require.Equal(t, "acdc://orphan:body", (<-stream).Content)
}

func TestDiscoverResources_LegacyCompatibility(t *testing.T) {
	root := t.TempDir()
	resourcesDir := content.NewContentProvider(root).ResourcesDir
	writeFile(t, resourcesDir, "nested/guide.md", "---\nname: Guide\ndescription: Existing resource\n---\n# Guide\n")
	writeFile(t, resourcesDir, "invalid.md", "# No required frontmatter\n")

	definitions, err := DiscoverResources(content.NewContentProvider(root), "acdc")
	require.NoError(t, err)
	require.Len(t, definitions, 1)
	require.Equal(t, "acdc://nested/guide", definitions[0].URI)
}

func newLookupProvider(t *testing.T) *ResourceProvider {
	t.Helper()
	sources := []domain.SourceDocument{
		{ID: "acdc://guide", URI: "acdc://guide", Name: "Guide", RelativePath: "docs/guide.md", Content: "# Guide", Keywords: []string{"setup"}, PathLabels: []string{"docs"}},
		{ID: "acdc://readme", URI: "acdc://readme", Name: "Readme", RelativePath: "README.md", Content: "# Readme"},
	}
	chunks := []domain.Chunk{
		{ID: "acdc://guide#guide", SourceID: "acdc://guide", SourceURI: "acdc://guide", ChunkURI: "acdc://guide#guide", SectionFragment: "guide", Fragment: "guide", Content: "# Guide"},
	}
	provider, err := NewResourceProvider(sources, chunks)
	require.NoError(t, err)
	return provider
}

func TestResourceProvider_Source_LookupByURI(t *testing.T) {
	provider := newLookupProvider(t)
	tests := []struct {
		name     string
		uri      string
		wantOK   bool
		wantName string
	}{
		{name: "source uri", uri: "acdc://guide", wantOK: true, wantName: "Guide"},
		{name: "second source uri", uri: "acdc://readme", wantOK: true, wantName: "Readme"},
		{name: "chunk uri is not a source", uri: "acdc://guide#guide", wantOK: false},
		{name: "section uri is not a source", uri: "acdc://guide#missing", wantOK: false},
		{name: "unknown uri", uri: "acdc://missing", wantOK: false},
		{name: "empty uri", uri: "", wantOK: false},
		{name: "relative path is not a uri", uri: "docs/guide.md", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := provider.Source(tt.uri)
			require.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				require.Equal(t, domain.SourceDocument{}, got)
				return
			}
			require.Equal(t, tt.uri, got.URI)
			require.Equal(t, tt.wantName, got.Name)
		})
	}
}

func TestResourceProvider_Source_ReturnsFullDocument(t *testing.T) {
	provider := newLookupProvider(t)

	got, ok := provider.Source("acdc://guide")

	require.True(t, ok)
	require.Equal(t, domain.SourceDocument{
		ID: "acdc://guide", URI: "acdc://guide", Name: "Guide", RelativePath: "docs/guide.md", Content: "# Guide",
		Keywords: []string{"setup"}, PathLabels: []string{"docs"},
	}, got)
}

func TestResourceProvider_Source_ReturnsClone(t *testing.T) {
	provider := newLookupProvider(t)

	first, ok := provider.Source("acdc://guide")
	require.True(t, ok)
	first.Keywords[0] = "mutated"
	first.PathLabels[0] = "mutated"
	first.Name = "mutated"

	second, ok := provider.Source("acdc://guide")
	require.True(t, ok)
	require.Equal(t, []string{"setup"}, second.Keywords)
	require.Equal(t, []string{"docs"}, second.PathLabels)
	require.Equal(t, "Guide", second.Name)
}

func TestResourceProvider_Source_IsolatedFromConstructorInput(t *testing.T) {
	keywords := []string{"setup"}
	provider, err := NewResourceProvider([]domain.SourceDocument{{ID: "acdc://guide", URI: "acdc://guide", RelativePath: "guide.md", Keywords: keywords}}, nil)
	require.NoError(t, err)
	keywords[0] = "mutated"

	got, ok := provider.Source("acdc://guide")

	require.True(t, ok)
	require.Equal(t, []string{"setup"}, got.Keywords)
}

func TestResourceProvider_SourceByPath_LookupByRelativePath(t *testing.T) {
	provider := newLookupProvider(t)
	tests := []struct {
		name    string
		path    string
		wantOK  bool
		wantURI string
	}{
		{name: "nested path", path: "docs/guide.md", wantOK: true, wantURI: "acdc://guide"},
		{name: "root path", path: "README.md", wantOK: true, wantURI: "acdc://readme"},
		{name: "unknown path", path: "docs/missing.md", wantOK: false},
		{name: "path match is exact", path: "./docs/guide.md", wantOK: false},
		{name: "path match is case-sensitive", path: "docs/Guide.md", wantOK: false},
		{name: "uri is not a path", path: "acdc://guide", wantOK: false},
		{name: "empty path", path: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := provider.SourceByPath(tt.path)
			require.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				require.Equal(t, domain.SourceDocument{}, got)
				return
			}
			require.Equal(t, tt.wantURI, got.URI)
			require.Equal(t, tt.path, got.RelativePath)
		})
	}
}

func TestResourceProvider_SourceByPath_ReturnsClone(t *testing.T) {
	provider := newLookupProvider(t)

	first, ok := provider.SourceByPath("docs/guide.md")
	require.True(t, ok)
	first.Keywords[0] = "mutated"

	second, ok := provider.SourceByPath("docs/guide.md")
	require.True(t, ok)
	require.Equal(t, []string{"setup"}, second.Keywords)
}

func TestResourceProvider_SourceByPath_UsesNativeSeparatorsAsSlashKey(t *testing.T) {
	native := filepath.Join("docs", "nested", "guide.md")
	provider, err := NewResourceProvider([]domain.SourceDocument{{ID: "acdc://nested", URI: "acdc://nested", RelativePath: native}}, nil)
	require.NoError(t, err)

	got, ok := provider.SourceByPath("docs/nested/guide.md")

	require.True(t, ok)
	require.Equal(t, "acdc://nested", got.URI)
}
