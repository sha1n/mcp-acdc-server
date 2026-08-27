package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

// TestInvalidFrontmatter_ServerStartsAndIndexesSiblings drives a real server
// over stdio against a manifest-configured content root holding one document
// whose frontmatter is invalid YAML -- an unquoted colon inside an unquoted
// value, the way a description written in prose breaks. The server must
// complete the MCP handshake and serve every other document. Before this
// behavior existed the process exited during discovery and the client saw only
// a failed handshake, with no indication of which file caused it.
func TestInvalidFrontmatter_ServerStartsAndIndexesSiblings(t *testing.T) {
	client := testkit.NewStdioTestClient(t, &testkit.ContentDirOptions{
		Metadata: `server:
  name: repo-docs
  version: "1"
  instructions: search docs
index:
  include: ["docs/**/*.md"]
`,
		Files: map[string]string{
			"docs/handoff.md": "---\n" +
				"description: The closed CI test-time series: what shipped\n" +
				"---\n# Handoff\n\nBody.\n",
			"docs/platform/auth.md": "# Authentication\n\n## Rotating API Keys\n\nRotate keys safely.\n",
		},
	})
	defer client.Close()
	ctx := context.Background()

	initResult := client.InitializeResult()
	require.NotNil(t, initResult, "server must complete the handshake despite the malformed document")

	resources, err := client.ListResources(ctx)
	require.NoError(t, err)
	require.Len(t, resources.Resources, 1)
	require.Equal(t, "acdc://docs/platform/auth", resources.Resources[0].URI)

	searchText := callTextTool(t, ctx, client, "search", map[string]any{"query": "rotate api keys"})
	require.Contains(t, searchText, "docs/platform/auth.md")
}

// TestInvalidFrontmatter_RefreshKeepsServingLaterEdits proves the mid-session
// half of the same contract. Revalidate abandons an entire refresh cycle on any
// error discovery returns, so a malformed document appearing under a running
// server used to freeze the catalog at its last-known-good snapshot: every
// later edit to every other document silently stopped taking effect. The
// malformed file must cost only itself, leaving the refresh free to publish
// its siblings.
func TestInvalidFrontmatter_RefreshKeepsServingLaterEdits(t *testing.T) {
	contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Metadata: `server:
  name: repo-docs
  version: "1"
  instructions: search docs
index:
  include: ["docs/**/*.md"]
`,
		Files: map[string]string{
			"docs/guide.md": "# Guide\n\n## Setup\n\nInstall dependencies before running the guide.\n",
		},
	})

	client := testkit.NewStdioTestClientForDir(t, contentDir)
	defer client.Close()
	ctx := context.Background()

	const term = "crystalburst"

	searchText := callTextTool(t, ctx, client, "search", map[string]any{"query": term})
	require.Equal(t, "No results found for '"+term+"'", searchText)

	// The malformed document and the new one land together, so the refresh
	// that discovers the second must survive the first.
	writeFile(t, contentDir, "docs/broken.md", "---\ndescription: A series: what shipped\n---\n# Broken\n")
	writeFile(t, contentDir, "docs/new.md", "# New Document\n\n## Details\n\nThe unique term "+term+" only appears in this section.\n")

	require.Eventually(t, func() bool {
		result, err := client.CallTool(ctx, "search", map[string]any{"query": term})
		if err != nil {
			return false
		}
		return strings.Contains(firstTextContentOrEmpty(result), "docs/new.md")
	}, refreshTimeout, refreshTick, "a malformed sibling must not stop the refresh from publishing docs/new.md")

	require.Equal(t, []string{"acdc://docs/guide", "acdc://docs/new"}, resourceURIs(t, ctx, client))
}
