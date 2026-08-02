package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

// refreshTimeout and refreshTick bound the require.Eventually polling loops
// below. The debounce interval under test is 2 seconds; 10 seconds gives a
// loaded CI machine enough headroom that the timeout signals a genuine
// failure to refresh rather than scheduling jitter, and the polling itself
// (calling search) is what drives the debounced refresh forward rather than
// a fixed sleep.
const (
	refreshTimeout = 10 * time.Second
	refreshTick    = 250 * time.Millisecond
)

// TestRefresh_NewDocumentBecomesSearchableMidSession proves a document
// written after the server has started is discovered, indexed and
// registered without a restart: search is unable to find a term before the
// file exists, finds it once the debounced refresh has run, and both read
// and resources/list agree the new document is present.
func TestRefresh_NewDocumentBecomesSearchableMidSession(t *testing.T) {
	contentDir := t.TempDir()
	writeFile(t, contentDir, "README.md", "# Sample Repo\n\nOverview text for the refresh test fixture.\n")
	writeFile(t, contentDir, "docs/a.md", "# Guide A\n\n"+
		"## Setup\n\nInstall dependencies before running Guide A procedures.\n")

	client := testkit.NewStdioTestClientForDir(t, contentDir)
	defer client.Close()
	ctx := context.Background()

	const term = "crystalburst"

	// Precondition snapshot, asserted once rather than polled: the term
	// cannot be found before the document containing it exists.
	searchText := callTextTool(t, ctx, client, "search", map[string]any{"query": term})
	require.Equal(t, fmt.Sprintf("No results found for '%s'", term), searchText)

	writeFile(t, contentDir, "docs/new.md", "# New Document\n\n"+
		"## Details\n\nThe unique term "+term+" only appears in this section.\n")

	var found string
	require.Eventually(t, func() bool {
		result, err := client.CallTool(ctx, "search", map[string]any{"query": term})
		if err != nil {
			return false
		}
		text := firstTextContentOrEmpty(result)
		if !strings.Contains(text, "docs/new.md") {
			return false
		}
		found = text
		return true
	}, refreshTimeout, refreshTick, "expected docs/new.md to become searchable after the debounced refresh")

	uri := extractFirstMarkdownURI(t, found)
	require.True(t, strings.HasPrefix(uri, "acdc://docs/new"), "expected a docs/new resource URI, got %s", uri)

	readText := callTextTool(t, ctx, client, "read", map[string]any{"uri": uri})
	require.Contains(t, readText, term)

	require.Contains(t, resourceURIs(t, ctx, client), "acdc://docs/new")
}

// TestRefresh_DeletedDocumentDisappears proves a document removed from the
// content root mid-session stops being searchable, stops being readable and
// drops out of resources/list — the half of reconciliation that exercises
// ResourceRegistrar.Sync's RemoveResources path rather than AddResource.
func TestRefresh_DeletedDocumentDisappears(t *testing.T) {
	contentDir := t.TempDir()
	writeFile(t, contentDir, "README.md", "# Sample Repo\n\nOverview text for the refresh test fixture.\n")
	const relativePath = "docs/a.md"
	writeFile(t, contentDir, relativePath, "# Guide A\n\n"+
		"## Setup\n\nThe unique term driftwoodanchor marks Guide A's setup section.\n")

	client := testkit.NewStdioTestClientForDir(t, contentDir)
	defer client.Close()
	ctx := context.Background()

	const term = "driftwoodanchor"

	// Precondition snapshot, asserted once: the term is findable before the
	// file is deleted.
	searchText := callTextTool(t, ctx, client, "search", map[string]any{"query": term})
	require.Contains(t, searchText, "docs/a.md")

	require.NoError(t, os.Remove(filepath.Join(contentDir, filepath.FromSlash(relativePath))))

	require.Eventually(t, func() bool {
		result, err := client.CallTool(ctx, "search", map[string]any{"query": term})
		if err != nil {
			return false
		}
		return firstTextContentOrEmpty(result) == fmt.Sprintf("No results found for '%s'", term)
	}, refreshTimeout, refreshTick, "expected docs/a.md to stop matching after the debounced refresh")

	requireReadFails(t, ctx, client, "acdc://docs/a")
	require.NotContains(t, resourceURIs(t, ctx, client), "acdc://docs/a")
}

// TestRefresh_EditedDocumentIsSearchableAndResourceSetUnchanged proves a
// content edit to an existing document is picked up by search without
// disturbing the registered resource set: the document keeps its URI, so
// ResourceRegistrar.Sync must diff it as unchanged rather than remove and
// re-add it.
func TestRefresh_EditedDocumentIsSearchableAndResourceSetUnchanged(t *testing.T) {
	contentDir := t.TempDir()
	writeFile(t, contentDir, "README.md", "# Sample Repo\n\nOverview text for the refresh test fixture.\n")
	writeFile(t, contentDir, "docs/a.md", "# Guide A\n\n"+
		"## Setup\n\nInstall dependencies before running Guide A procedures.\n")

	client := testkit.NewStdioTestClientForDir(t, contentDir)
	defer client.Close()
	ctx := context.Background()

	beforeURIs := resourceURIs(t, ctx, client)

	const term = "molybdenumfalcon"
	// The rewritten file is longer than the original and contains a
	// different term, so both the cheap walk's size digest and the content
	// digest are guaranteed to move - a same-size rewrite could otherwise
	// race the filesystem's mtime resolution and be missed by the cheap
	// walk gate.
	writeFile(t, contentDir, "docs/a.md", "# Guide A\n\n"+
		"## Setup\n\nInstall dependencies before running Guide A procedures.\n\n"+
		"## Updated\n\nThe unique term "+term+" now appears in this revised section.\n")

	require.Eventually(t, func() bool {
		result, err := client.CallTool(ctx, "search", map[string]any{"query": term})
		if err != nil {
			return false
		}
		return strings.Contains(firstTextContentOrEmpty(result), "docs/a.md")
	}, refreshTimeout, refreshTick, "expected the edited docs/a.md content to become searchable after the debounced refresh")

	require.ElementsMatch(t, beforeURIs, resourceURIs(t, ctx, client))
}

// resourceURIs returns the URIs currently in the server's resources/list
// response.
func resourceURIs(t *testing.T, ctx context.Context, client *testkit.TestClient) []string {
	t.Helper()
	result, err := client.ListResources(ctx)
	require.NoError(t, err)
	uris := make([]string, 0, len(result.Resources))
	for _, r := range result.Resources {
		uris = append(uris, r.URI)
	}
	return uris
}

// requireReadFails asserts that reading uri through the read tool fails,
// either as a protocol-level error or as a result flagged IsError - the SDK
// client can surface a tool handler's returned error either way.
func requireReadFails(t *testing.T, ctx context.Context, client *testkit.TestClient, uri string) {
	t.Helper()
	result, err := client.CallTool(ctx, "read", map[string]any{"uri": uri})
	if err != nil {
		return
	}
	require.NotNil(t, result)
	require.True(t, result.IsError, "expected read of %s to fail", uri)
}

// firstTextContentOrEmpty extracts the first text content item from a tool
// result, or "" if there is none. Unlike the package's require-based text
// helpers, it makes no test assertions, so it is safe to call from inside a
// require.Eventually condition, which testify runs on a goroutine other than
// the test's own.
func firstTextContentOrEmpty(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	for _, item := range result.Content {
		if content, ok := item.(*mcp.TextContent); ok {
			return content.Text
		}
	}
	return ""
}
