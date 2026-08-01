package integration

import (
	"context"
	"regexp"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

// TestConfiguredSearch_ReferencesAndFragmentRead verifies configured discovery, chunk
// search in the default "references" result mode, and chunk-fragment read through a
// real stdio MCP session.
func TestConfiguredSearch_ReferencesAndFragmentRead(t *testing.T) {
	client := testkit.NewStdioTestClient(t, &testkit.ContentDirOptions{
		Metadata: `server:
  name: repo-docs
  version: "1"
  instructions: search docs
index:
  include: ["docs/**/*.md"]
  exclude: ["docs/generated/**"]
`,
		Files: map[string]string{
			"docs/platform/auth.md":     "# Authentication\n\n## Rotating API Keys\n\nRotate keys safely.\n",
			"docs/generated/ignored.md": "# Ignored\n\nRotate keys unsafely.\n",
		},
	})
	defer client.Close()
	ctx := context.Background()

	resources, err := client.ListResources(ctx)
	require.NoError(t, err)
	require.Len(t, resources.Resources, 1)
	require.Equal(t, "acdc://docs/platform/auth", resources.Resources[0].URI)

	searchText := callTextTool(t, ctx, client, "search", map[string]any{"query": "rotate api keys"})
	require.Contains(t, searchText, "docs/platform/auth.md")
	require.Contains(t, searchText, "Authentication > Rotating API Keys")
	require.NotContains(t, searchText, "ignored")

	chunkURI := extractFirstMarkdownURI(t, searchText)
	chunkText := callTextTool(t, ctx, client, "read", map[string]any{"uri": chunkURI})
	require.Contains(t, chunkText, "Rotate keys safely.")
	require.NotContains(t, chunkText, "# Authentication")
}

func callTextTool(t *testing.T, ctx context.Context, client *testkit.TestClient, name string, arguments map[string]any) string {
	t.Helper()
	result, err := client.CallTool(ctx, name, arguments)
	require.NoError(t, err)
	for _, item := range result.Content {
		if content, ok := item.(*mcp.TextContent); ok {
			return content.Text
		}
	}
	t.Fatal("tool result did not contain text content")
	return ""
}

var markdownURIRegexp = regexp.MustCompile(`\((acdc://[^)]+)\)`)

func extractFirstMarkdownURI(t *testing.T, text string) string {
	t.Helper()
	match := markdownURIRegexp.FindStringSubmatch(text)
	require.Len(t, match, 2)
	return match[1]
}

// TestConfiguredSearch_ContentModeIncludesFullChunkBody verifies that the process-wide
// "content" result mode, requested through ContentDirOptions.ResultMode, causes the
// search tool itself to return the exact matched chunk body without a follow-up read.
func TestConfiguredSearch_ContentModeIncludesFullChunkBody(t *testing.T) {
	client := testkit.NewStdioTestClient(t, &testkit.ContentDirOptions{
		ResultMode: "content",
		Metadata: `server:
  name: repo-docs
  version: "1"
  instructions: search docs
index:
  include: ["docs/**/*.md"]
`,
		Files: map[string]string{
			"docs/platform/auth.md": "# Authentication\n\n## Rotating API Keys\n\nRotate keys safely.\n",
		},
	})
	defer client.Close()
	ctx := context.Background()

	searchText := callTextTool(t, ctx, client, "search", map[string]any{"query": "rotate api keys"})
	require.Contains(t, searchText, "## Rotating API Keys\n\nRotate keys safely.")
}

// TestConfiguredSearch_LegacyDiscoveryUnaffected verifies that content directories with
// no index: section keep the pre-existing legacy behavior end to end: legacy resource
// URIs, prompts, the unchanged search/read tool input schemas, full-document reads, and
// cross-reference link rewriting all continue to work through a real stdio MCP session.
func TestConfiguredSearch_LegacyDiscoveryUnaffected(t *testing.T) {
	client := testkit.NewStdioTestClient(t, &testkit.ContentDirOptions{
		CrossRef: true,
		Resources: map[string]string{
			"doc-a.md": "---\nname: Doc A\ndescription: First doc\n---\nSee [Doc B](doc-b.md) for details.\n",
			"doc-b.md": "---\nname: Doc B\ndescription: Second doc\n---\nBack to [Doc A](doc-a.md).\n",
		},
		Prompts: map[string]string{
			"greet.md": "---\nname: greet\ndescription: Greeting prompt\narguments:\n  - name: who\n    description: who to greet\n    required: true\n---\nHello {{.who}}",
		},
	})
	defer client.Close()
	ctx := context.Background()

	// Legacy resource URIs remain <scheme>://<relative-path-without-extension>.
	resourcesResult, err := client.ListResources(ctx)
	require.NoError(t, err)
	uris := make([]string, 0, len(resourcesResult.Resources))
	for _, r := range resourcesResult.Resources {
		uris = append(uris, r.URI)
	}
	require.Contains(t, uris, "acdc://doc-a")
	require.Contains(t, uris, "acdc://doc-b")

	// The search/read tool input schemas remain unchanged: query only, uri only.
	toolsResult, err := client.ListTools(ctx)
	require.NoError(t, err)
	schemasByTool := make(map[string]any)
	for _, tool := range toolsResult.Tools {
		schemasByTool[tool.Name] = tool.InputSchema
	}
	require.ElementsMatch(t, []string{"query"}, schemaPropertyNames(t, schemasByTool["search"]))
	require.ElementsMatch(t, []string{"uri"}, schemaPropertyNames(t, schemasByTool["read"]))

	// The prompt still works.
	promptsResult, err := client.ListPrompts(ctx)
	require.NoError(t, err)
	require.Len(t, promptsResult.Prompts, 1)
	require.Equal(t, "greet", promptsResult.Prompts[0].Name)

	promptResult, err := client.GetPrompt(ctx, "greet", map[string]string{"who": "ACDC"})
	require.NoError(t, err)
	require.Len(t, promptResult.Messages, 1)

	// Full-document read strips frontmatter and rewrites the cross-referenced link.
	readText := callTextTool(t, ctx, client, "read", map[string]any{"uri": "acdc://doc-a"})
	require.Contains(t, readText, "See [Doc B](acdc://doc-b) for details.")
	require.NotContains(t, readText, "---")
}

// schemaPropertyNames extracts the top-level "properties" keys from a client-observed
// tool input schema, which the SDK exposes as the default JSON marshaling of the
// server's schema (a map[string]any).
func schemaPropertyNames(t *testing.T, schema any) []string {
	t.Helper()
	schemaMap, ok := schema.(map[string]any)
	require.True(t, ok, "expected input schema to be a map[string]any, got %T", schema)

	properties, ok := schemaMap["properties"].(map[string]any)
	require.True(t, ok, "expected input schema to have a properties map")

	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	return names
}
