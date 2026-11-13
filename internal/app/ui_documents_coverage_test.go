package app

import (
	"path/filepath"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/stretchr/testify/require"
)

func TestCatalogDocuments_ResolvePathAcceptsColonInFileName(t *testing.T) {
	sources := []domain.SourceDocument{
		{ID: "acdc://docs/1a", URI: "acdc://docs/1a", Name: "Digit", FilePath: filepath.Join(uiTestRoot, "docs", "1a:notes.md"), RelativePath: filepath.Join("docs", "1a:notes.md"), Content: "# Digit\n"},
		{ID: "acdc://docs/v2", URI: "acdc://docs/v2", Name: "Space", FilePath: filepath.Join(uiTestRoot, "docs", "my notes:v2.md"), RelativePath: filepath.Join("docs", "my notes:v2.md"), Content: "# Space\n"},
	}
	provider, err := resources.NewResourceProvider(sources, nil)
	require.NoError(t, err)
	snapshot := newCatalogDocuments(resources.NewCatalogHolder(provider)).Snapshot()

	tests := []struct {
		name    string
		target  string
		wantURI string
	}{
		{name: "scheme starting with digit", target: "1a:notes.md", wantURI: "acdc://docs/1a"},
		{name: "scheme containing space", target: "my notes:v2.md", wantURI: "acdc://docs/v2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uri, ok := snapshot.ResolvePath("docs/index.md", tt.target)

			require.True(t, ok)
			require.Equal(t, tt.wantURI, uri)
		})
	}
}
