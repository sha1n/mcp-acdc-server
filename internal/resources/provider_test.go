package resources

import (
	"context"
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
	writeFile(t, root, "mcp-resources/nested/guide.md", "---\nname: Guide\ndescription: Existing resource\n---\n# Guide\n")
	writeFile(t, root, "mcp-resources/invalid.md", "# No required frontmatter\n")

	definitions, err := DiscoverResources(content.NewContentProvider(root), "acdc")
	require.NoError(t, err)
	require.Len(t, definitions, 1)
	require.Equal(t, "acdc://nested/guide", definitions[0].URI)
}
