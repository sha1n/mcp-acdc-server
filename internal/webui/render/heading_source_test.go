package render_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sha1n/mcp-acdc-server/internal/webui/render"
)

func renderedHeadingIDs(rendered string) []string {
	ids := []string{}
	for _, match := range headingIDPattern.FindAllStringSubmatch(rendered, -1) {
		ids = append(ids, match[1])
	}
	return ids
}

func renderDocument(markdown, headingSource string) string {
	return render.Markdown(markdown, render.Options{
		FromPath: fromPath, HeadingIDs: true, HeadingSource: headingSource, Links: newFakeResolver(nil),
	})
}

// Each authored source pairs with the cross-reference-transformed text the
// document is rendered from: identical except for rewritten link destinations.
var headingSourceCases = []struct {
	name        string
	authored    string
	transformed string
	wantIDs     []string
}{
	{
		name:        "inline code link syntax",
		authored:    "# Guide\n\n## Use `[Guide](other.md)`\n\nBody.\n",
		transformed: "# Guide\n\n## Use `[Guide](acdc://other)`\n\nBody.\n",
		wantIDs:     []string{"guide", "use-guideothermd"},
	},
	{
		name:        "escaped opening bracket",
		authored:    "# Guide\n\n## \\[Guide](other.md)\n\nBody.\n",
		transformed: "# Guide\n\n## \\[Guide](acdc://other)\n\nBody.\n",
		wantIDs:     []string{"guide", "guideothermd"},
	},
	{
		name:        "escaped closing bracket",
		authored:    "# Guide\n\n## [literal\\](other.md)\n\nBody.\n",
		transformed: "# Guide\n\n## [literal\\](acdc://other)\n\nBody.\n",
		wantIDs:     []string{"guide", "literalothermd"},
	},
	{
		name:        "duplicate headings",
		authored:    "# Guide\n\n## \\[Guide](other.md)\n\nOne.\n\n## \\[Guide](other.md)\n\nTwo.\n",
		transformed: "# Guide\n\n## \\[Guide](acdc://other)\n\nOne.\n\n## \\[Guide](acdc://other)\n\nTwo.\n",
		wantIDs:     []string{"guide", "guideothermd", "guideothermd-1"},
	},
}

func TestMarkdown_HeadingSourceAssignsAuthoredIDsInDocumentOrder(t *testing.T) {
	for _, tc := range headingSourceCases {
		t.Run(tc.name, func(t *testing.T) {
			rendered := renderDocument(tc.transformed, tc.authored)

			require.Equal(t, tc.wantIDs, renderedHeadingIDs(rendered))
			require.Equal(t, renderedHeadingIDs(renderDocument(tc.authored, "")), renderedHeadingIDs(rendered))
			require.Contains(t, rendered, "acdc://other", "the body still renders from the transformed markdown")
		})
	}
}

func TestMarkdown_HeadingSourceKeepsOrdinaryLinkHeadingIDs(t *testing.T) {
	authored := "# Guide\n\n## See [Other](other.md)\n\nBody.\n"
	transformed := "# Guide\n\n## See [Other](acdc://other)\n\nBody.\n"

	rendered := renderDocument(transformed, authored)

	require.Equal(t, []string{"guide", "see-other"}, renderedHeadingIDs(rendered))
}

func TestMarkdown_EmptyHeadingSourceAllocatesFromRenderedMarkdown(t *testing.T) {
	transformed := "# Guide\n\n## \\[Guide](acdc://other)\n\nBody.\n"

	rendered := renderDocument(transformed, "")

	require.Equal(t, []string{"guide", "guideacdcother"}, renderedHeadingIDs(rendered))
	require.Equal(t, render.Markdown(transformed, render.Options{FromPath: fromPath, HeadingIDs: true, Links: newFakeResolver(nil)}), rendered)
}

func TestMarkdown_HeadingSourceIgnoredForPreviews(t *testing.T) {
	authored := "## \\[Guide](other.md)\n\nBody.\n"
	transformed := "## \\[Guide](acdc://other)\n\nBody.\n"
	preview := func(headingSource string) string {
		return render.Markdown(transformed, render.Options{
			FromPath: fromPath, SourceURI: "acdc://docs/guide", HeadingSource: headingSource, Links: newFakeResolver(nil),
		})
	}

	withSource := preview(authored)

	require.Empty(t, renderedHeadingIDs(withSource))
	require.Equal(t, preview(""), withSource)
}

var headingTextPattern = regexp.MustCompile(`<h[1-6]\b[^>]*\bid="([^"]*)"[^>]*>([^<]*)</h[1-6]>`)

type renderedHeading struct {
	Text string
	ID   string
}

func renderedHeadings(rendered string) []renderedHeading {
	headings := []renderedHeading{}
	for _, match := range headingTextPattern.FindAllStringSubmatch(rendered, -1) {
		headings = append(headings, renderedHeading{Text: match[2], ID: match[1]})
	}
	return headings
}

func requireUniqueHeadingIDs(t *testing.T, rendered string) {
	t.Helper()
	seen := map[string]bool{}
	for _, id := range renderedHeadingIDs(rendered) {
		require.False(t, seen[id], "duplicate heading id %q in %s", id, rendered)
		seen[id] = true
	}
}

// Each case keeps the authored line structure (the transformer only rewrites
// link destinations within a line) while the served block structure drifts,
// so headings pair by source line and level, never by position.
var headingStructuralDriftCases = []struct {
	name        string
	authored    string
	transformed string
	want        []renderedHeading
	absentIDs   []string
}{
	{
		name:        "transform swallows a heading into an html comment",
		authored:    "# Top\n\n<!-- [x](-->/../other.md)\n\n## Lost\n\n<!-- -->\n\n## Kept\n",
		transformed: "# Top\n\n<!-- [x](acdc://other)\n\n## Lost\n\n<!-- -->\n\n## Kept\n",
		want:        []renderedHeading{{"Top", "top"}, {"Kept", "kept"}},
		absentIDs:   []string{"lost"},
	},
	{
		name: "transform swaps a hidden heading for an exposed one at the same level",
		authored: "# Top\n\n<!-- [x](-->/../../other.md)\n\n## Lost\n\n<!-- -->\n\n" +
			"<!-- [x](other.md)\n\n## Gained\n\n<!-- -->\n\n## Kept\n",
		transformed: "# Top\n\n<!-- [x](acdc://other)\n\n## Lost\n\n<!-- -->\n\n" +
			"<!-- [x](acdc://-->/other)\n\n## Gained\n\n<!-- -->\n\n## Kept\n",
		want:      []renderedHeading{{"Top", "top"}, {"Gained", "gained"}, {"Kept", "kept"}},
		absentIDs: []string{"lost"},
	},
	{
		name:        "transform swallows the first of duplicate headings",
		authored:    "# Top\n\n<!-- [x](-->/../other.md)\n\n## Repeat\n\n<!-- -->\n\n## Repeat\n",
		transformed: "# Top\n\n<!-- [x](acdc://other)\n\n## Repeat\n\n<!-- -->\n\n## Repeat\n",
		want:        []renderedHeading{{"Top", "top"}, {"Repeat", "repeat-1"}},
		absentIDs:   []string{"repeat"},
	},
	{
		name:        "rendered markdown exposes an extra heading",
		authored:    "# Top\n\nText.\n\n## Kept\n",
		transformed: "# Top\n\n## Added\n\n## Kept\n",
		want:        []renderedHeading{{"Top", "top"}, {"Added", "added"}, {"Kept", "kept"}},
	},
	{
		name:        "exposed heading never takes a removed heading's id",
		authored:    "# Top\n\n## Lost\n\nText.\n",
		transformed: "# Top\n\nText.\n\n## Lost\n",
		want:        []renderedHeading{{"Top", "top"}, {"Lost", "lost-1"}},
		absentIDs:   []string{"lost"},
	},
	{
		name:        "same line with a different level",
		authored:    "# Top\n\n## Second\n\n## Third\n",
		transformed: "# Top\n\n### Beta\n\n## Gamma\n",
		want:        []renderedHeading{{"Top", "top"}, {"Beta", "beta"}, {"Gamma", "third"}},
		absentIDs:   []string{"second"},
	},
}

func TestMarkdown_HeadingSourceStructuralDriftPairsHeadingsBySourceLine(t *testing.T) {
	for _, tc := range headingStructuralDriftCases {
		t.Run(tc.name, func(t *testing.T) {
			rendered := renderDocument(tc.transformed, tc.authored)

			require.Equal(t, tc.want, renderedHeadings(rendered), rendered)
			requireUniqueHeadingIDs(t, rendered)
			for _, id := range tc.absentIDs {
				require.NotContains(t, renderedHeadingIDs(rendered), id, "a removed section's id is never reassigned")
			}
		})
	}
}

func TestMarkdown_HeadingSourceLineCountMismatchAllocatesFromRenderedMarkdown(t *testing.T) {
	authored := "# Top\n\n## \\[Guide](other.md)\n"
	transformed := "# Top\n\n## Added\n\n## \\[Guide](acdc://other)\n"

	rendered := renderDocument(transformed, authored)

	require.Equal(t, renderDocument(transformed, ""), rendered)
	require.Equal(t, []renderedHeading{{"Top", "top"}, {"Added", "added"}, {"[Guide](acdc://other)", "guideacdcother"}}, renderedHeadings(rendered))
}
