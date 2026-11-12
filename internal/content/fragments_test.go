package content

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

type headingID struct {
	text     string
	nested   bool
	id       string
	assigned bool
}

func TestHeadingIDs_AssignsIDsInDocumentOrder(t *testing.T) {
	cases := []struct {
		name     string
		markdown string
		want     []headingID
	}{
		{
			name:     "top-level headings use slugs with duplicate suffixes",
			markdown: "# Configuration\n\n## Authentication\n\n## Authentication\n\n### Authentication\n",
			want: []headingID{
				{text: "Configuration", id: "configuration", assigned: true},
				{text: "Authentication", id: "authentication", assigned: true},
				{text: "Authentication", id: "authentication-1", assigned: true},
				{text: "Authentication", id: "authentication-2", assigned: true},
			},
		},
		{
			name:     "document is reserved for the preamble",
			markdown: "Intro.\n\n# Document\n\n# Document\n",
			want: []headingID{
				{text: "Document", id: "document-1", assigned: true},
				{text: "Document", id: "document-2", assigned: true},
			},
		},
		{
			name:     "setext headings and inline formatting",
			markdown: "Getting *Started*\n=================\n\nNext `Step`\n-----------\n",
			want: []headingID{
				{text: "Getting Started", id: "getting-started", assigned: true},
				{text: "Next Step", id: "next-step", assigned: true},
			},
		},
		{
			name:     "text without slug characters falls back to section",
			markdown: "# !!!\n\n# ???\n",
			want: []headingID{
				{text: "!!!", id: "section", assigned: true},
				{text: "???", id: "section-1", assigned: true},
			},
		},
		{
			name:     "text-less headings get no id and consume no fragment",
			markdown: "#\n\n# Real\n\n##   \n\n# Real\n",
			want: []headingID{
				{text: ""},
				{text: "Real", id: "real", assigned: true},
				{text: ""},
				{text: "Real", id: "real-1", assigned: true},
			},
		},
		{
			name:     "nested list heading never takes a later top-level fragment",
			markdown: "- ## Setup\n\n## Setup\n\nBody.\n",
			want: []headingID{
				{text: "Setup", nested: true, id: "setup-1", assigned: true},
				{text: "Setup", id: "setup", assigned: true},
			},
		},
		{
			name:     "nested headings are allocated after every top-level heading",
			markdown: "> # Note\n\n# Note\n\n- # Note\n\n# Note\n",
			want: []headingID{
				{text: "Note", nested: true, id: "note-2", assigned: true},
				{text: "Note", id: "note", assigned: true},
				{text: "Note", nested: true, id: "note-3", assigned: true},
				{text: "Note", id: "note-1", assigned: true},
			},
		},
		{
			name:     "deeply nested headings in blockquote within list",
			markdown: "- item\n\n  > ### Deep Heading\n\n# Deep Heading\n",
			want: []headingID{
				{text: "Deep Heading", nested: true, id: "deep-heading-1", assigned: true},
				{text: "Deep Heading", id: "deep-heading", assigned: true},
			},
		},
		{
			name:     "nested heading named document is not the preamble",
			markdown: "> # Document\n",
			want: []headingID{
				{text: "Document", nested: true, id: "document-1", assigned: true},
			},
		},
		{
			name:     "fenced code is not a heading",
			markdown: "```\n# Not a heading\n```\n\n# Not a heading\n",
			want: []headingID{
				{text: "Not a heading", id: "not-a-heading", assigned: true},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := []byte(tc.markdown)
			doc := parseGFM(source)

			ids := HeadingIDs(doc, source)

			require.Equal(t, tc.want, describeHeadingIDs(doc, source, ids))
			require.Len(t, ids, countAssigned(tc.want), "only headings with text receive ids")
		})
	}
}

func TestHeadingIDs_EmptyDocumentHasNoIDs(t *testing.T) {
	source := []byte("")
	require.Empty(t, HeadingIDs(parseGFM(source), source))
}

func TestHeadingAnchor_MapsChunkFragmentToHeadingID(t *testing.T) {
	cases := []struct {
		fragment string
		want     string
	}{
		{fragment: "install", want: "install"},
		{fragment: "install~2", want: "install"},
		{fragment: "install~10", want: "install"},
		{fragment: "install-1", want: "install-1"},
		{fragment: "install-1~3", want: "install-1"},
		{fragment: "document", want: ""},
		{fragment: "document~1", want: ""},
		{fragment: "document~12", want: ""},
		{fragment: "document-1", want: "document-1"},
		{fragment: "document-1~2", want: "document-1"},
		{fragment: "", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.fragment, func(t *testing.T) {
			require.Equal(t, tc.want, HeadingAnchor(tc.fragment))
		})
	}
}

func TestHeadingAnchor_AgreesWithHeadingIDsForEveryChunk(t *testing.T) {
	large := strings.Repeat("word ", 450)
	documents := map[string]string{
		"preamble and duplicates": "Intro.\n\n# Configuration\nText.\n\n## Authentication\nKeys.\n\n## Authentication\nTokens.\n",
		"split section":           "# Large\n\n" + large + "\n\n" + large + "\n\n" + large + "\n",
		"split preamble":          large + "\n\n" + large + "\n\n# After\nBody.\n",
		"reserved document":       "Intro.\n\n# Document\nBody.\n\n## Document\nMore.\n",
		"nested before top-level": "- ## Setup\n\n## Setup\nBody.\n\n> ## Usage\n\n## Usage\nMore.\n",
		"heading without body":    "# Empty\n\n# Full\nBody.\n\n# Empty\nNow with body.\n",
		"text-less headings":      "#\nOrphan.\n\n# Named\nBody.\n",
		"setext":                  "Title\n=====\n\nBody.\n\nSub\n---\n\nMore.\n",
	}

	for name, markdown := range documents {
		t.Run(name, func(t *testing.T) {
			raw := []byte(markdown)
			parsed, err := ParseMarkdown(raw, FrontmatterOptional)
			require.NoError(t, err)
			source, err := BuildSourceDocument(parsed, SourceOptions{URI: "acdc://docs/x", RelativePath: "docs/x.md", Raw: raw})
			require.NoError(t, err)
			chunks, err := ChunkMarkdown(source, parsed)
			require.NoError(t, err)
			require.NotEmpty(t, chunks)

			doc := parseGFM(parsed.Body)
			idByLine := topLevelIDsByLine(doc, parsed.Body, HeadingIDs(doc, parsed.Body))
			firstPartAnchor := map[string]string{}

			for _, chunk := range chunks {
				anchor := HeadingAnchor(chunk.Fragment)
				preamble := chunk.SectionFragment == "document"
				require.Equal(t, preamble, anchor == "", "chunk %q", chunk.Fragment)
				if preamble {
					continue
				}
				if chunk.Part == 1 {
					id, ok := idByLine[chunk.StartLine]
					require.True(t, ok, "chunk %q must start at a top-level heading with an id", chunk.Fragment)
					require.Equal(t, id, anchor, "chunk %q", chunk.Fragment)
					firstPartAnchor[chunk.SectionFragment] = anchor
					continue
				}
				require.Equal(t, firstPartAnchor[chunk.SectionFragment], anchor, "split part %q must anchor at its section heading", chunk.Fragment)
			}
		})
	}
}

func TestHeadingIDs_SplitSectionExercisesMultipleParts(t *testing.T) {
	large := strings.Repeat("word ", 450)
	chunks := chunkMarkdownForTest(t, []byte("# Large\n\n"+large+"\n\n"+large+"\n\n"+large+"\n"))
	require.Greater(t, len(chunks), 1)
	for _, chunk := range chunks {
		require.Equal(t, "large", HeadingAnchor(chunk.Fragment))
	}
}

func parseGFM(source []byte) ast.Node {
	return goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(source))
}

func describeHeadingIDs(doc ast.Node, source []byte, ids map[*ast.Heading]string) []headingID {
	var described []headingID
	for _, heading := range collectHeadings(doc) {
		id, assigned := ids[heading]
		described = append(described, headingID{
			text:     strings.TrimSpace(headingText(heading, source)),
			nested:   heading.Parent() == nil || heading.Parent().Kind() != ast.KindDocument,
			id:       id,
			assigned: assigned,
		})
	}
	return described
}

func collectHeadings(doc ast.Node) []*ast.Heading {
	var headings []*ast.Heading
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if heading, ok := node.(*ast.Heading); ok && entering {
			headings = append(headings, heading)
		}
		return ast.WalkContinue, nil
	})
	return headings
}

func headingText(heading *ast.Heading, source []byte) string {
	var out strings.Builder
	_ = ast.Walk(heading, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch typed := node.(type) {
		case *ast.Text:
			out.Write(typed.Segment.Value(source))
		case *ast.String:
			out.Write(typed.Value)
		}
		return ast.WalkContinue, nil
	})
	return out.String()
}

func topLevelIDsByLine(doc ast.Node, source []byte, ids map[*ast.Heading]string) map[int]string {
	byLine := map[int]string{}
	for node := doc.FirstChild(); node != nil; node = node.NextSibling() {
		heading, ok := node.(*ast.Heading)
		if !ok || heading.Lines().Len() == 0 {
			continue
		}
		if id, assigned := ids[heading]; assigned {
			line := 1 + bytes.Count(source[:heading.Lines().At(0).Start], []byte("\n"))
			byLine[line] = id
		}
	}
	return byLine
}

func countAssigned(headings []headingID) int {
	count := 0
	for _, heading := range headings {
		if heading.assigned {
			count++
		}
	}
	return count
}
