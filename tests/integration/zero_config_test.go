package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

// defaultInstructionsFormat mirrors internal/domain/metadata.go's unexported
// defaultInstructionsFormat verbatim. It is duplicated here, rather than derived
// from the production default, so this test independently pins the exact wording a
// zero-config client receives instead of merely echoing whatever the production
// constant currently says.
const defaultInstructionsFormat = `Documentation for the %s repository: guides, references, design documents, specs and plans kept under docs/, plus the top-level README.

Search here before answering questions about this repository's conventions, architecture, decisions, or planned work. Prefer these documents over assumptions drawn from source code alone.`

// TestZeroConfig_RepoWithDocs drives a real server, over the in-process stdio
// transport, rooted at a temp directory with no mcp-metadata.yaml but with the
// default zero-config layout (README.md plus docs/**/*.md). It proves the seam
// from defaults through discovery, chunking, indexing, and the MCP surface: the
// derived server identity, chunk-scoped search, bare and fragment reads, and the
// resource listing.
func TestZeroConfig_RepoWithDocs(t *testing.T) {
	contentDir := t.TempDir()
	writeFile(t, contentDir, "README.md", "# Acme Sample Repo\n\n"+
		"## Overview\n\nThis repository hosts the Acme sample project used for documentation testing.\n\n"+
		"## Getting Started\n\nClone the repository and run make install to get started.\n")
	writeFile(t, contentDir, "docs/a.md", "# Guide A\n\n"+
		"## Setup\n\nInstall dependencies before running Guide A procedures.\n\n"+
		"## Usage\n\nRun the guide A workflow after setup completes.\n")
	writeFile(t, contentDir, "docs/nested/b.md", "# Nested Guide B\n\n"+
		"## Configuration\n\nThe zephyrwing setting controls nested-guide caching behavior.\n\n"+
		"## Troubleshooting\n\nRestart the service if nested guide B does not start.\n")
	// These three files must NOT be indexed, each for a different reason, so
	// resources/list below proves the default patterns and lenient prune list
	// haven't silently widened.
	writeFile(t, contentDir, "CONTRIBUTING.md", "# Contributing\n\n"+
		"## Pull Requests\n\nOpen a pull request against main and describe your change.\n")
	writeFile(t, contentDir, "src/notes.md", "# Developer Notes\n\n"+
		"## Scratch\n\nInternal notes that live alongside source, not documentation.\n")
	writeFile(t, contentDir, "docs/build/generated.md", "# Generated Output\n\n"+
		"## Build Artifacts\n\nContent written by a build step, not authored documentation.\n")

	client := testkit.NewStdioTestClientForDir(t, contentDir)
	defer client.Close()
	ctx := context.Background()

	repoBaseName := filepath.Base(contentDir)

	// The server starts and derives its identity from the content root's base name,
	// per the built-in zero-config defaults.
	initResult := client.InitializeResult()
	require.NotNil(t, initResult)
	require.Equal(t, repoBaseName+" Documentation", initResult.ServerInfo.Name)
	require.Equal(t, fmt.Sprintf(defaultInstructionsFormat, repoBaseName), initResult.Instructions)

	// A term unique to docs/nested/b.md returns a chunk URI from that file.
	searchText := callTextTool(t, ctx, client, "search", map[string]any{"query": "zephyrwing"})
	require.Contains(t, searchText, "docs/nested/b.md")

	chunkURI := extractFirstMarkdownURI(t, searchText)
	require.Contains(t, chunkURI, "docs/nested/b#")

	// The fragment URI from search resolves to the matched section's content.
	chunkText := callTextTool(t, ctx, client, "read", map[string]any{"uri": chunkURI})
	require.Contains(t, chunkText, "zephyrwing setting controls nested-guide caching behavior")

	// The bare document URI resolves to the whole document, including sections the
	// chunk read above did not return.
	docText := callTextTool(t, ctx, client, "read", map[string]any{"uri": "acdc://docs/nested/b"})
	require.Contains(t, docText, "# Nested Guide B")
	require.Contains(t, docText, "## Troubleshooting")

	// resources/list surfaces exactly the three default-included documents. The
	// exact-set match also proves CONTRIBUTING.md, src/notes.md, and
	// docs/build/generated.md are excluded: a widened include pattern or a
	// missing prune entry would add one of them to this set and fail the match.
	resourcesResult, err := client.ListResources(ctx)
	require.NoError(t, err)
	uris := make([]string, 0, len(resourcesResult.Resources))
	for _, r := range resourcesResult.Resources {
		uris = append(uris, r.URI)
	}
	require.ElementsMatch(t, []string{"acdc://README", "acdc://docs/a", "acdc://docs/nested/b"}, uris)
}

// TestZeroConfig_RepoWithNoDocs verifies that a repository with no manifest and no
// README.md or docs/ still starts successfully, and that searching it returns the
// ordinary no-results message rather than an error.
func TestZeroConfig_RepoWithNoDocs(t *testing.T) {
	contentDir := t.TempDir()

	client := testkit.NewStdioTestClientForDir(t, contentDir)
	defer client.Close()
	ctx := context.Background()

	require.NotNil(t, client.InitializeResult())

	searchText := callTextTool(t, ctx, client, "search", map[string]any{"query": "anything"})
	require.Equal(t, "No results found for 'anything'", searchText)
}

// TestZeroConfig_LegacyResourcesLayoutIsNotDiscovered verifies that a content
// root laid out for legacy discovery (mcp-resources/**.md) but missing
// mcp-metadata.yaml does not fall back to legacy discovery. Zero-config
// defaults always set a non-nil Index, which routes discovery to the
// configured-index path (README.md and docs/**) instead of scanning
// mcp-resources. The fixture file carries valid frontmatter, so it would have
// been discovered under legacy mode - proving the file is discoverable in
// principle, and still isn't found.
func TestZeroConfig_LegacyResourcesLayoutIsNotDiscovered(t *testing.T) {
	contentDir := t.TempDir()
	writeFile(t, contentDir, "mcp-resources/guide.md", "---\nname: Guide\ndescription: A guide.\n---\n"+
		"# Guide\n\nContent for the guide.\n")

	client := testkit.NewStdioTestClientForDir(t, contentDir)
	defer client.Close()
	ctx := context.Background()

	require.NotNil(t, client.InitializeResult())

	resourcesResult, err := client.ListResources(ctx)
	require.NoError(t, err)
	require.Empty(t, resourcesResult.Resources)

	searchText := callTextTool(t, ctx, client, "search", map[string]any{"query": "guide"})
	require.Equal(t, "No results found for 'guide'", searchText)
}

func writeFile(t *testing.T, root, relativePath, content string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(relativePath))
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte(content), 0o644))
}
