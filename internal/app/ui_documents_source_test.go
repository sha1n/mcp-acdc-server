package app

import (
	"path/filepath"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/stretchr/testify/require"
)

const authoredGuideBody = "# Guide\n\n## \\[Guide](other.md)\n\nSee [Other](other.md).\n"

func crossRefSourceDiscovery() resources.DiscoveryResult {
	return resources.DiscoveryResult{Sources: []domain.SourceDocument{
		{
			ID: "acdc://docs/guide", URI: "acdc://docs/guide", Name: "Guide",
			FilePath:     filepath.Join(uiTestRoot, "docs", "guide.md"),
			RelativePath: filepath.Join("docs", "guide.md"),
			Content:      authoredGuideBody,
		},
		{
			ID: "acdc://docs/other", URI: "acdc://docs/other", Name: "Other",
			FilePath:     filepath.Join(uiTestRoot, "docs", "other.md"),
			RelativePath: filepath.Join("docs", "other.md"),
			Content:      "# Other\n",
		},
	}}
}

func TestCatalogDocuments_DocumentCarriesAuthoredSourceAndServedContent(t *testing.T) {
	provider, err := assembleResourceProvider(crossRefSourceDiscovery(), true, "acdc")
	require.NoError(t, err)
	holder := resources.NewCatalogHolder(provider)
	mcpBody, err := holder.ReadResource("acdc://docs/guide")
	require.NoError(t, err)

	doc, err := newCatalogDocuments(holder).Snapshot().Document("acdc://docs/guide")

	require.NoError(t, err)
	require.Equal(t, authoredGuideBody, doc.Source)
	require.Equal(t, mcpBody, doc.Content)
	require.Contains(t, doc.Content, "[Other](acdc://docs/other)")
	require.NotEqual(t, doc.Source, doc.Content)
}
