package integration

import (
	"context"
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
