package mcp

import (
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/stretchr/testify/require"
)

func TestSelectSearchResults_ReferencesOnePerSource(t *testing.T) {
	results := []search.SearchResult{
		{ChunkID: "a1", SourceID: "a", SourceURI: "acdc://a", ChunkURI: "acdc://a#one", Score: 5},
		{ChunkID: "a2", SourceID: "a", SourceURI: "acdc://a", ChunkURI: "acdc://a#two", Score: 4},
		{ChunkID: "b1", SourceID: "b", SourceURI: "acdc://b", ChunkURI: "acdc://b#one", Score: 3},
	}

	selected := selectSearchResults(results, config.SearchResultModeReferences, 10)

	require.Equal(t, []string{"a1", "b1"}, selectedIDs(selected))
}

func TestSelectSearchResults_ContentCapsPerSourceAndGlobal(t *testing.T) {
	results := []search.SearchResult{
		{ChunkID: "a1", SourceID: "a"},
		{ChunkID: "a2", SourceID: "a"},
		{ChunkID: "a3", SourceID: "a"},
		{ChunkID: "b1", SourceID: "b"},
	}

	selected := selectSearchResults(results, config.SearchResultModeContent, 3)

	require.Equal(t, []string{"a1", "a2", "b1"}, selectedIDs(selected))
}

func TestFormatSearchResults_Provenance(t *testing.T) {
	result := search.SearchResult{
		SourceTitle: "Configuration",
		SourcePath:  "docs/reference/config.md",
		HeadingPath: []string{"Authentication", "API Keys"},
		ChunkURI:    "acdc://docs/reference/config#api-keys",
		StartLine:   84,
		EndLine:     112,
		Score:       3.5,
		Snippet:     "rotate <mark>keys</mark>",
		Content:     "Rotate keys safely.",
	}

	reference := formatSearchResults("keys", []search.SearchResult{result}, config.SearchResultModeReferences)
	require.Contains(t, reference, "docs/reference/config.md")
	require.Contains(t, reference, "Authentication > API Keys")
	require.Contains(t, reference, "lines 84-112")
	require.NotContains(t, reference, "Rotate keys safely.")

	content := formatSearchResults("keys", []search.SearchResult{result}, config.SearchResultModeContent)
	require.Contains(t, content, "Rotate keys safely.")
}

func selectedIDs(results []search.SearchResult) []string {
	ids := make([]string, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.ChunkID)
	}
	return ids
}
