package integration

import (
	"context"
	"html"
	"net/url"
	"regexp"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

func crossRefHeadingFixture() map[string]string {
	return map[string]string{
		"guide.md": "---\nname: Guide\ndescription: Guide\nkeywords: [crossrefheading]\n---\n" +
			"# Guide\n\nIntro.\n\n" +
			"## Use `[Guide](other.md)`\n\nCrossrefheadingmarker inline code.\n\n" +
			"## \\[Guide](other.md)\n\nCrossrefheadingmarker escaped opening bracket.\n\n" +
			"## [literal\\](other.md)\n\nCrossrefheadingmarker escaped closing bracket.\n\n" +
			"## See [Other](other.md)\n\nCrossrefheadingmarker ordinary link.\n\n" +
			"## \\[Guide](other.md)\n\nCrossrefheadingmarker duplicate heading.\n\n" +
			"See [Other](other.md).\n",
		"other.md": "---\nname: Other\ndescription: Other\n---\n# Other\n\nOther body.\n",
	}
}

func readResourceOverSSE(t *testing.T, baseURL, uri string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "webui-crossref-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.SSEClientTransport{Endpoint: baseURL + "/sse"}, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	result, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
	require.NoError(t, err)
	require.Len(t, result.Contents, 1)
	return result.Contents[0].Text
}

func TestWebUIAPI_CrossRefHeadingFragmentsResolve(t *testing.T) {
	baseURL, _ := testkit.StartSSE(t,
		&testkit.ContentDirOptions{Resources: crossRefHeadingFixture()},
		&testkit.FlagOptions{UIEnabled: true, CrossRef: true})
	pageIDs := headingIDs(webUIDocumentByURI(t, baseURL, "acdc://guide").ContentHTML)

	resp := webUISearch(t, baseURL, url.Values{"q": {"crossrefheadingmarker"}, "limit": {"100"}})

	fragments := map[string]bool{}
	for _, r := range resp.Results {
		uri, fragment := parseDocumentURL(t, r.DocumentURL)
		if uri != "acdc://guide" || fragment == "" {
			continue
		}
		fragments[fragment] = true
		require.Contains(t, pageIDs, fragment, "result %s", r.ChunkURI)
	}
	require.Equal(t, map[string]bool{
		"use-guideothermd": true,
		"guideothermd":     true,
		"literalothermd":   true,
		"see-other":        true,
		"guideothermd-1":   true,
	}, fragments)
	require.Len(t, pageIDs, len(uniqueStrings(pageIDs)), "heading ids are unique: %v", pageIDs)
}

func uniqueStrings(values []string) map[string]bool {
	set := map[string]bool{}
	for _, v := range values {
		set[v] = true
	}
	return set
}

func TestWebUIAPI_CrossRefDocumentShowsBodyAsServedToMCP(t *testing.T) {
	baseURL, _ := testkit.StartSSE(t,
		&testkit.ContentDirOptions{Resources: crossRefHeadingFixture()},
		&testkit.FlagOptions{UIEnabled: true, CrossRef: true})

	doc := webUIDocumentByURI(t, baseURL, "acdc://guide")

	require.Equal(t, readResourceOverSSE(t, baseURL, "acdc://guide"), doc.Content)
	requireDocumentLinksResolve(t, baseURL, doc.ContentHTML)
}

func TestWebUIAPI_CrossRefMCPReadMatchesLegacyTransformer(t *testing.T) {
	baseURL, _ := testkit.StartSSE(t,
		&testkit.ContentDirOptions{Resources: crossRefHeadingFixture()},
		&testkit.FlagOptions{UIEnabled: true, CrossRef: true})

	body := readResourceOverSSE(t, baseURL, "acdc://guide")

	require.Contains(t, body, "## Use `[Guide](acdc://other)`")
	require.Contains(t, body, "## \\[Guide](acdc://other)")
	require.Contains(t, body, "## [literal\\](acdc://other)")
	require.Contains(t, body, "## See [Other](acdc://other)")
	require.NotContains(t, body, "(other.md)")
}

var headingIDByTextRe = regexp.MustCompile(`<h[1-6]\b[^>]*\bid="([^"]*)"[^>]*>([^<]*)</h[1-6]>`)

const crossRefDriftFrontmatter = "---\nname: Doc\ndescription: Doc\n---\n"

type headingIDByText struct {
	Text string
	ID   string
}

// Each fixture's cross-reference rewrite drops a "-->" from a link destination
// inside an html comment, so the served block structure differs from the
// authored one while every line keeps its position.
var crossRefStructuralDriftCases = []struct {
	name      string
	resources map[string]string
	guideURI  string
	want      []headingIDByText
	absentIDs []string
}{
	{
		name: "served comment swallows a heading",
		resources: map[string]string{
			"guide.md": crossRefDriftFrontmatter + "# Top\n\n<!-- [x](-->/../other.md)\n\n## Lost\n\n<!-- -->\n\n## Kept\n",
			"other.md": crossRefDriftFrontmatter + "# Other\n",
		},
		guideURI:  "acdc://guide",
		want:      []headingIDByText{{"Top", "top"}, {"Kept", "kept"}},
		absentIDs: []string{"lost"},
	},
	{
		name: "served comments swap a hidden heading for an exposed one at the same level",
		resources: map[string]string{
			"-->/guide.md": crossRefDriftFrontmatter + "# Top\n\n<!-- [x](-->/../../other.md)\n\n## Lost\n\n<!-- -->\n\n" +
				"<!-- [x](other.md)\n\n## Gained\n\n<!-- -->\n\n## Kept\n",
			"-->/other.md": crossRefDriftFrontmatter + "# Nested Other\n",
			"other.md":     crossRefDriftFrontmatter + "# Other\n",
		},
		guideURI:  "acdc://-->/guide",
		want:      []headingIDByText{{"Top", "top"}, {"Gained", "gained"}, {"Kept", "kept"}},
		absentIDs: []string{"lost"},
	},
	{
		name: "served comment swallows the first of duplicate headings",
		resources: map[string]string{
			"guide.md": crossRefDriftFrontmatter + "# Top\n\n<!-- [x](-->/../other.md)\n\n## Repeat\n\n<!-- -->\n\n## Repeat\n",
			"other.md": crossRefDriftFrontmatter + "# Other\n",
		},
		guideURI:  "acdc://guide",
		want:      []headingIDByText{{"Top", "top"}, {"Repeat", "repeat-1"}},
		absentIDs: []string{"repeat"},
	},
}

func TestWebUIAPI_CrossRefStructuralDriftKeepsHeadingIDsOnTheirOwnSections(t *testing.T) {
	for _, tc := range crossRefStructuralDriftCases {
		t.Run(tc.name, func(t *testing.T) {
			baseURL, _ := testkit.StartSSE(t,
				&testkit.ContentDirOptions{Resources: tc.resources},
				&testkit.FlagOptions{UIEnabled: true, CrossRef: true})

			contentHTML := webUIDocumentByURI(t, baseURL, tc.guideURI).ContentHTML

			headings := []headingIDByText{}
			for _, m := range headingIDByTextRe.FindAllStringSubmatch(contentHTML, -1) {
				headings = append(headings, headingIDByText{Text: html.UnescapeString(m[2]), ID: html.UnescapeString(m[1])})
			}
			require.Equal(t, tc.want, headings, contentHTML)
			pageIDs := headingIDs(contentHTML)
			for _, id := range tc.absentIDs {
				require.NotContains(t, pageIDs, id, "a removed section's id is never reassigned")
			}
			require.Len(t, pageIDs, len(uniqueStrings(pageIDs)), "heading ids are unique: %v", pageIDs)
		})
	}
}
