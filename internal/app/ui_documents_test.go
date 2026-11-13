package app

import (
	"path/filepath"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/webui"
	"github.com/stretchr/testify/require"
)

// uiTestRoot is absolute because the cross-ref transformer keys sources by FilePath.
var uiTestRoot = filepath.Join(string(filepath.Separator), "content")

func uiTestSources() []domain.SourceDocument {
	return []domain.SourceDocument{
		{
			ID: "acdc://docs/guide", URI: "acdc://docs/guide", Name: "Guide",
			FilePath:     filepath.Join(uiTestRoot, "docs", "guide.md"),
			RelativePath: filepath.Join("docs", "guide.md"),
			Content:      "# Guide\n\nSee [Other](other.md) and [Readme](../README.md).\n",
			Keywords:     []string{"Setup", "docs"}, PathLabels: []string{"docs", " Guides "},
		},
		{ID: "acdc://docs/other", URI: "acdc://docs/other", Name: "Other", FilePath: filepath.Join(uiTestRoot, "docs", "other.md"), RelativePath: filepath.Join("docs", "other.md"), Content: "# Other\n"},
		{ID: "acdc://docs/nested/deep", URI: "acdc://docs/nested/deep", Name: "Deep", FilePath: filepath.Join(uiTestRoot, "docs", "nested", "deep.md"), RelativePath: filepath.Join("docs", "nested", "deep.md"), Content: "# Deep\n"},
		{ID: "acdc://README", URI: "acdc://README", Name: "Readme", FilePath: filepath.Join(uiTestRoot, "README.md"), RelativePath: "README.md", Content: "# Readme\n"},
	}
}

func uiTestChunks() []domain.Chunk {
	return []domain.Chunk{{
		ID: "acdc://docs/guide#guide", SourceID: "acdc://docs/guide", SourceURI: "acdc://docs/guide",
		ChunkURI: "acdc://docs/guide#guide", SectionFragment: "guide", Fragment: "guide", Content: "# Guide",
	}}
}

func newUITestHolder(t *testing.T, opts ...resources.Option) *resources.CatalogHolder {
	t.Helper()
	provider, err := resources.NewResourceProvider(uiTestSources(), uiTestChunks(), opts...)
	require.NoError(t, err)
	return resources.NewCatalogHolder(provider)
}

func TestCatalogDocuments_DocumentReturnsSourceAsServedToMCP(t *testing.T) {
	holder := newUITestHolder(t)

	doc, err := newCatalogDocuments(holder).Snapshot().Document("acdc://docs/guide")

	require.NoError(t, err)
	mcpBody, err := holder.ReadResource("acdc://docs/guide")
	require.NoError(t, err)
	require.Equal(t, webui.Document{
		URI:     "acdc://docs/guide",
		Title:   "Guide",
		Path:    "docs/guide.md",
		Labels:  []string{"docs", "guides", "setup"},
		Content: mcpBody,
		Source:  uiTestSources()[0].Content,
	}, doc)
}

func TestCatalogDocuments_DocumentLabelsNeverNil(t *testing.T) {
	doc, err := newCatalogDocuments(newUITestHolder(t)).Snapshot().Document("acdc://README")

	require.NoError(t, err)
	require.NotNil(t, doc.Labels)
	require.Empty(t, doc.Labels)
	require.Equal(t, "README.md", doc.Path)
}

func TestCatalogDocuments_DocumentAppliesCrossRefExactlyAsMCP(t *testing.T) {
	holder := newUITestHolder(t, resources.WithTransformer(resources.NewCrossRefTransformer(uiTestSources(), "acdc")))

	doc, err := newCatalogDocuments(holder).Snapshot().Document("acdc://docs/guide")

	require.NoError(t, err)
	mcpBody, err := holder.ReadResource("acdc://docs/guide")
	require.NoError(t, err)
	require.Equal(t, mcpBody, doc.Content)
	require.Contains(t, doc.Content, "acdc://docs/other")
	require.NotContains(t, doc.Content, "(other.md)")
}

func TestCatalogDocuments_DocumentRejectsUnknownAndNonSourceURIs(t *testing.T) {
	snapshot := newCatalogDocuments(newUITestHolder(t)).Snapshot()
	tests := []struct {
		name string
		uri  string
	}{
		{name: "unknown uri", uri: "acdc://missing"},
		{name: "chunk uri", uri: "acdc://docs/guide#guide"},
		{name: "section uri", uri: "acdc://docs/guide#setup"},
		{name: "empty uri", uri: ""},
		{name: "relative path", uri: "docs/guide.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := snapshot.Document(tt.uri)

			require.ErrorIs(t, err, webui.ErrDocumentNotFound)
			require.Equal(t, webui.Document{}, doc)
		})
	}
}

func TestCatalogDocuments_ResolvePath(t *testing.T) {
	snapshot := newCatalogDocuments(newUITestHolder(t)).Snapshot()
	tests := []struct {
		name     string
		fromPath string
		relPath  string
		wantURI  string
		wantOK   bool
	}{
		{name: "sibling path", fromPath: "docs/guide.md", relPath: "other.md", wantURI: "acdc://docs/other", wantOK: true},
		{name: "dot-relative path", fromPath: "docs/guide.md", relPath: "./other.md", wantURI: "acdc://docs/other", wantOK: true},
		{name: "parent path", fromPath: "docs/guide.md", relPath: "../README.md", wantURI: "acdc://README", wantOK: true},
		{name: "child path", fromPath: "docs/guide.md", relPath: "nested/deep.md", wantURI: "acdc://docs/nested/deep", wantOK: true},
		{name: "path through parent and back", fromPath: "docs/nested/deep.md", relPath: "../../docs/other.md", wantURI: "acdc://docs/other", wantOK: true},
		{name: "redundant segments cleaned", fromPath: "docs/guide.md", relPath: "nested/../other.md", wantURI: "acdc://docs/other", wantOK: true},
		{name: "from root document", fromPath: "README.md", relPath: "docs/guide.md", wantURI: "acdc://docs/guide", wantOK: true},
		{name: "resource uri is not a path", fromPath: "docs/guide.md", relPath: "acdc://README"},
		{name: "missing path", fromPath: "docs/guide.md", relPath: "missing.md"},
		{name: "path without extension", fromPath: "docs/guide.md", relPath: "other"},
		{name: "path escaping catalog root", fromPath: "README.md", relPath: "../README.md"},
		{name: "path escaping then re-entering", fromPath: "docs/guide.md", relPath: "../../README.md"},
		{name: "absolute path", fromPath: "docs/guide.md", relPath: "/README.md"},
		{name: "empty path", fromPath: "docs/guide.md", relPath: ""},
		{name: "directory path", fromPath: "docs/guide.md", relPath: "."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uri, ok := snapshot.ResolvePath(tt.fromPath, tt.relPath)

			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.wantURI, uri)
		})
	}
}

func TestCatalogDocuments_ResolveURI(t *testing.T) {
	snapshot := newCatalogDocuments(newUITestHolder(t)).Snapshot()
	tests := []struct {
		name    string
		uri     string
		wantURI string
		wantOK  bool
	}{
		{name: "resource uri", uri: "acdc://README", wantURI: "acdc://README", wantOK: true},
		{name: "nested resource uri", uri: "acdc://docs/nested/deep", wantURI: "acdc://docs/nested/deep", wantOK: true},
		{name: "unknown resource uri", uri: "acdc://missing"},
		{name: "chunk resource uri", uri: "acdc://docs/guide#guide"},
		{name: "foreign scheme", uri: "https://example.com/docs/other.md"},
		{name: "relative path is not a uri", uri: "docs/other.md"},
		{name: "empty uri", uri: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uri, ok := snapshot.ResolveURI(tt.uri)

			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.wantURI, uri)
		})
	}
}

func TestCatalogDocuments_SnapshotFollowsCatalogSwap(t *testing.T) {
	holder := newUITestHolder(t)
	reader := newCatalogDocuments(holder)
	before := reader.Snapshot()

	replacement, err := resources.NewResourceProvider([]domain.SourceDocument{
		{ID: "acdc://docs/guide", URI: "acdc://docs/guide", Name: "Guide v2", RelativePath: "docs/guide.md", Content: "# Guide v2\n"},
		{ID: "acdc://docs/added", URI: "acdc://docs/added", Name: "Added", RelativePath: "docs/added.md", Content: "# Added\n"},
	}, nil)
	require.NoError(t, err)
	holder.Swap(replacement)
	after := reader.Snapshot()

	doc, err := after.Document("acdc://docs/guide")
	require.NoError(t, err)
	require.Equal(t, "Guide v2", doc.Title)
	require.Equal(t, "# Guide v2\n", doc.Content)
	_, err = after.Document("acdc://docs/other")
	require.ErrorIs(t, err, webui.ErrDocumentNotFound)
	uri, ok := after.ResolvePath("docs/guide.md", "added.md")
	require.True(t, ok)
	require.Equal(t, "acdc://docs/added", uri)
	_, ok = after.ResolvePath("docs/guide.md", "other.md")
	require.False(t, ok)

	stale, err := before.Document("acdc://docs/guide")
	require.NoError(t, err)
	require.Equal(t, "Guide", stale.Title)
	_, ok = before.ResolvePath("docs/guide.md", "added.md")
	require.False(t, ok, "a snapshot is pinned to the catalog generation it captured")
	uri, ok = before.ResolvePath("docs/guide.md", "other.md")
	require.True(t, ok)
	require.Equal(t, "acdc://docs/other", uri)
}
