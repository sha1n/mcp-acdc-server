package content

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const autoLinkHeadingDoc = "Intro text.\n\n" +
	"# Overview\n\nA.\n\n" +
	"# <https://example.com>\n\nB.\n\n" +
	"## https://example.com\n\nC.\n\n" +
	"# Overview\n\nD.\n\n" +
	"> # www.x.io\n"

func TestHeadingIDs_AssignsIDsToAutoLinkOnlyHeadings(t *testing.T) {
	cases := []struct {
		name     string
		markdown string
		want     []headingID
	}{
		{
			name:     "angle autolink heading uses its label slug",
			markdown: "# <https://example.com>\n",
			want:     []headingID{{text: "", id: "httpsexamplecom", assigned: true}},
		},
		{
			name:     "bare autolink heading uses its label slug",
			markdown: "## www.x.io\n",
			want:     []headingID{{text: "", id: "wwwxio", assigned: true}},
		},
		{
			name:     "autolink headings are allocated after catalog headings",
			markdown: autoLinkHeadingDoc,
			want: []headingID{
				{text: "Overview", id: "overview", assigned: true},
				{text: "", id: "httpsexamplecom", assigned: true},
				{text: "", id: "httpsexamplecom-1", assigned: true},
				{text: "Overview", id: "overview-1", assigned: true},
				{text: "", nested: true, id: "wwwxio", assigned: true},
			},
		},
		{
			name:     "catalog heading keeps its slug when an earlier autolink heading shares it",
			markdown: "# <https://example.com>\n\n# httpsexamplecom\n\n> ## httpsexamplecom\n",
			want: []headingID{
				{text: "", id: "httpsexamplecom-2", assigned: true},
				{text: "httpsexamplecom", id: "httpsexamplecom", assigned: true},
				{text: "httpsexamplecom", nested: true, id: "httpsexamplecom-1", assigned: true},
			},
		},
		{
			name:     "heading with text beside an autolink keeps its catalog id",
			markdown: "# See <https://example.com>\n",
			want:     []headingID{{text: "See", id: "see", assigned: true}},
		},
		{
			name:     "heading without visible text stays unassigned",
			markdown: "# \n",
			want:     []headingID{{text: "", id: "", assigned: false}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := []byte(tc.markdown)
			doc := parseGFM(source)

			ids := HeadingIDs(doc, source)

			require.Equal(t, tc.want, describeHeadingIDs(doc, source, ids))
		})
	}
}

func TestChunkMarkdown_AutoLinkOnlyHeadingsDoNotChangeChunks(t *testing.T) {
	raw := []byte(autoLinkHeadingDoc)
	parsed, err := ParseMarkdown(raw, FrontmatterOptional)
	require.NoError(t, err)
	source, err := BuildSourceDocument(parsed, SourceOptions{URI: "acdc://docs/autolink", RelativePath: "docs/autolink.md", Raw: raw})
	require.NoError(t, err)

	chunks, err := ChunkMarkdown(source, parsed)
	require.NoError(t, err)

	type chunkShape struct {
		id, fragment string
		headingPath  []string
		start, end   int
	}
	var got []chunkShape
	for _, chunk := range chunks {
		got = append(got, chunkShape{chunk.ID, chunk.Fragment, chunk.HeadingPath, chunk.StartLine, chunk.EndLine})
	}
	require.Equal(t, []chunkShape{
		{"acdc://docs/autolink#document", "document", nil, 1, 1},
		{"acdc://docs/autolink#overview", "overview", []string{"Overview"}, 3, 13},
		{"acdc://docs/autolink#overview-1", "overview-1", []string{"Overview"}, 15, 19},
	}, got)

	doc := parseGFM(parsed.Body)
	idByLine := topLevelIDsByLine(doc, parsed.Body, HeadingIDs(doc, parsed.Body))
	for _, chunk := range chunks[1:] {
		require.Equal(t, idByLine[chunk.StartLine], HeadingAnchor(chunk.Fragment), "chunk %s", chunk.ID)
	}
}
