package app

import (
	"errors"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/webui"
	"github.com/stretchr/testify/require"
)

func newTestCatalog(t *testing.T, sources ...domain.SourceDocument) *resources.ResourceProvider {
	t.Helper()
	provider, err := resources.NewResourceProvider(sources, nil)
	require.NoError(t, err)
	return provider
}

func TestCatalogDocumentReader_FollowsCatalogSwap(t *testing.T) {
	holder := resources.NewCatalogHolder(newTestCatalog(t, domain.SourceDocument{URI: "acdc://a", Name: "Old", Content: "old"}))
	reader := newCatalogDocumentReader(holder)

	holder.Swap(newTestCatalog(t, domain.SourceDocument{URI: "acdc://a", Name: "New", RelativePath: "a.md", Content: "new"}))

	doc, err := reader.Document("acdc://a")
	require.NoError(t, err)
	require.Equal(t, webui.Document{URI: "acdc://a", Title: "New", Path: "a.md", Content: "new", Labels: []string{}}, doc)
}

func TestCatalogDocumentReader_LabelsAreLowerCasedDedupedPathLabelsThenKeywords(t *testing.T) {
	holder := resources.NewCatalogHolder(newTestCatalog(t, domain.SourceDocument{
		URI:        "acdc://a",
		PathLabels: []string{"Reference", "search"},
		Keywords:   []string{"Search", "BLEVE", " ", "reference", "fuzzy"},
	}))

	doc, err := newCatalogDocumentReader(holder).Document("acdc://a")
	require.NoError(t, err)
	require.Equal(t, []string{"reference", "search", "bleve", "fuzzy"}, doc.Labels)
}

func TestCatalogDocumentReader_UnknownAndNonSourceURIsAreNotFound(t *testing.T) {
	holder := resources.NewCatalogHolder(newTestCatalog(t, domain.SourceDocument{URI: "acdc://a"}))
	reader := newCatalogDocumentReader(holder)

	for _, uri := range []string{"acdc://missing", "acdc://a#section"} {
		_, err := reader.Document(uri)
		require.True(t, errors.Is(err, webui.ErrDocumentNotFound), uri)
	}
}
