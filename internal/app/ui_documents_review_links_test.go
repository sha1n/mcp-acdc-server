package app

import (
	"path/filepath"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/webui/render"
	"github.com/stretchr/testify/require"
)

func newColonPathTestHolder(t *testing.T) *resources.CatalogHolder {
	t.Helper()
	sources := append(uiTestSources(), domain.SourceDocument{
		ID: "acdc://docs/notes-v2", URI: "acdc://docs/notes-v2", Name: "Notes v2",
		FilePath:     filepath.Join(uiTestRoot, "docs", "notes:v2.md"),
		RelativePath: filepath.Join("docs", "notes:v2.md"),
		Content:      "# Notes v2\n",
	})
	provider, err := resources.NewResourceProvider(sources, uiTestChunks())
	require.NoError(t, err)
	return resources.NewCatalogHolder(provider)
}

func TestCatalogDocuments_PercentEncodedColonLinkResolvesAsRelativePath(t *testing.T) {
	snapshot := newCatalogDocuments(newColonPathTestHolder(t)).Snapshot()
	tests := []struct {
		name     string
		markdown string
	}{
		{name: "bare relative path", markdown: "[v2](notes%3Av2.md)"},
		{name: "dot-relative path", markdown: "[v2](./notes%3Av2.md)"},
		{name: "with fragment", markdown: "[v2](notes%3Av2.md#notes-v2)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rendered := render.Markdown(tt.markdown, render.Options{FromPath: "docs/guide.md", HeadingIDs: true, Links: snapshot})

			require.NotContains(t, rendered, "broken-link")
			require.Contains(t, rendered, `href="/ui/doc?uri=acdc%3A%2F%2Fdocs%2Fnotes-v2`)
		})
	}
}
