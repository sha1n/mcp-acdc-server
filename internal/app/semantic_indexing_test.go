package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/embed"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/stretchr/testify/require"
)

// crossRefContentDir writes a content root whose first document carries a
// relative link to a second document that may or may not exist yet.
func crossRefContentDir(t *testing.T, withTarget bool) string {
	t.Helper()

	contentDir := t.TempDir()
	cp := content.NewContentProvider(contentDir)
	require.NoError(t, os.MkdirAll(cp.ConfigDir, 0o755))
	require.NoError(t, os.WriteFile(cp.ConfigFile, []byte(`
server:
  name: test
  version: "1.0"
  instructions: test
tools: []
index:
  include:
    - docs/**/*.md
`), 0o600))

	docs := filepath.Join(contentDir, "docs")
	require.NoError(t, os.MkdirAll(docs, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(docs, "linker.md"), []byte(
		"---\nname: Linker\ndescription: Links onward\n---\n\nSee [the target](target.md) for details.\n"), 0o600))

	if withTarget {
		require.NoError(t, os.WriteFile(filepath.Join(docs, "target.md"), []byte(
			"---\nname: Target\ndescription: The target\n---\n\nTarget body.\n"), 0o600))
	}
	return contentDir
}

func indexContentDir(t *testing.T, contentDir string, indexer search.Searcher) {
	t.Helper()

	ctx := context.Background()
	cp := content.NewContentProvider(contentDir)
	resolved, err := resolveMetadata(cp, "test")
	require.NoError(t, err)

	discovery, err := resources.Discover(ctx, cp, resolved.metadata.Index, "acdc")
	require.NoError(t, err)

	provider, err := assembleResourceProvider(discovery, true, "acdc")
	require.NoError(t, err)

	require.NoError(t, IndexResources(ctx, provider, indexer))
}

// Removing a document that another document links to changes the linking
// document's *streamed* text — the relative link no longer resolves to a
// resource URI — while its file bytes and its Fingerprint are untouched.
// Keying the embedding cache on Fingerprint would carry the stale vector
// forward silently. Keying on the exact embedder input cannot.
//
// The removal direction is what makes the assertion exact: the second pass
// indexes a strict subset of the first, so any embedding at all in that pass
// is necessarily the linking document. Adding the target instead would embed
// the new document unconditionally and mask a cache that never invalidates.
func TestSemanticIndexing_CrossRefChangeReEmbedsTheLinkingDocument(t *testing.T) {
	fake := embed.NewFake(8)
	settings := config.SearchSettings{
		InMemory:      true,
		MaxResults:    10,
		SemanticModel: "test-model",
		SemanticFloor: config.DefaultSemanticFloor,
	}
	lexical := search.NewService(settings)
	hybrid, err := search.NewHybridService(lexical, fake, settings)
	require.NoError(t, err)
	defer hybrid.Close()

	withTarget := crossRefContentDir(t, true)
	indexContentDir(t, withTarget, hybrid)
	require.Positive(t, fake.DocumentsEmbedded())

	fake.ResetCounts()
	withoutTarget := crossRefContentDir(t, false)
	indexContentDir(t, withoutTarget, hybrid)

	require.Positive(t, fake.DocumentsEmbedded(),
		"a cross-ref rewrite changes the embedded text, so the linking document must re-embed")
}

// The complement: with nothing changed at all, nothing re-embeds. Without
// this, the test above would pass on a cache that never works.
func TestSemanticIndexing_UnchangedContentReEmbedsNothing(t *testing.T) {
	fake := embed.NewFake(8)
	settings := config.SearchSettings{
		InMemory:      true,
		MaxResults:    10,
		SemanticModel: "test-model",
		SemanticFloor: config.DefaultSemanticFloor,
	}
	lexical := search.NewService(settings)
	hybrid, err := search.NewHybridService(lexical, fake, settings)
	require.NoError(t, err)
	defer hybrid.Close()

	contentDir := crossRefContentDir(t, true)
	indexContentDir(t, contentDir, hybrid)
	require.Positive(t, fake.DocumentsEmbedded())

	fake.ResetCounts()
	indexContentDir(t, contentDir, hybrid)

	require.Equal(t, 0, fake.DocumentsEmbedded(), "re-indexing unchanged content must embed nothing")
}
