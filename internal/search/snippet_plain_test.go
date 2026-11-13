package search

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/stretchr/testify/require"
)

const snippetEllipsis = "…"

func TestLabelledSearch_SnippetIsPlainTextProjectionOfMarkdown(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		query     string
		wantWords []string
		wantHits  []string
		absent    []string
	}{
		{
			name:      "heading markers",
			content:   "# Search Features\n\n## Writing Resources\n\nSearch is fast.",
			query:     "fast",
			wantWords: []string{"Search", "Features", "—", "Writing", "Resources", "—", "Search", "is", "fast."},
			wantHits:  []string{"fast"},
			absent:    []string{"#"},
		},
		{
			name:      "emphasis",
			content:   "Use **bold** and _italic_ and *star* and __strong__ text.",
			query:     "bold",
			wantWords: []string{"Use", "bold", "and", "italic", "and", "star", "and", "strong", "text."},
			wantHits:  []string{"bold"},
			absent:    []string{"*", "_"},
		},
		{
			name:      "inline code keeps code text",
			content:   "Run `make lint` before pushing.",
			query:     "lint",
			wantWords: []string{"Run", "make", "lint", "before", "pushing."},
			wantHits:  []string{"lint"},
			absent:    []string{"`"},
		},
		{
			name:      "link keeps text and drops url",
			content:   "Powered by [Bleve](https://blevesearch.com/) engine.",
			query:     "engine",
			wantWords: []string{"Powered", "by", "Bleve", "engine."},
			wantHits:  []string{"engine"},
			absent:    []string{"[", "]", "(", ")", "https", "blevesearch.com"},
		},
		{
			name:      "hit inside link text",
			content:   "Powered by [Bleve](https://example.com/docs) engine.",
			query:     "bleve",
			wantWords: []string{"Powered", "by", "Bleve", "engine."},
			wantHits:  []string{"Bleve"},
			absent:    []string{"example.com", "https"},
		},
		{
			name:      "table keeps cell text",
			content:   "| Field | Boost |\n|---|---|\n| title | high |\n| content | low |",
			query:     "boost",
			wantWords: []string{"Field", "·", "Boost", "/", "title", "·", "high", "/", "content", "·", "low"},
			wantHits:  []string{"Boost"},
			absent:    []string{"|", "---"},
		},
		{
			name:     "code fence keeps code text",
			content:  "Example:\n\n```yaml\nname: demo\nkind: widget\n```\n\nAfter the block.",
			query:    "widget",
			wantHits: []string{"widget"},
			absent:   []string{"```", "`"},
		},
		{
			name:      "tilde code fence keeps code text",
			content:   "~~~\nrun widget\n~~~",
			query:     "widget",
			wantWords: []string{"run", "widget"},
			wantHits:  []string{"widget"},
			absent:    []string{"~"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := snippetService(t, snippetChunk("one", "One", tt.content))

			spans := onlySnippet(t, service, tt.query, nil)

			requireCanonicalSpans(t, spans)
			joined := joinedSpans(spans)
			for _, marker := range tt.absent {
				require.NotContains(t, joined, marker, "snippet %q keeps markdown syntax", joined)
			}
			require.NotContains(t, joined, snippetEllipsis, "an untruncated snippet carries no ellipsis")
			if tt.wantWords != nil {
				require.Equal(t, tt.wantWords, strings.Fields(joined))
			}
			require.Equal(t, tt.wantHits, spanTexts(spans, true))
		})
	}
}

func TestLabelledSearch_SnippetCodeFenceKeepsCodeLines(t *testing.T) {
	content := "Example:\n\n```yaml\nname: demo\nkind: widget\n```\n\nAfter the block."
	service := snippetService(t, snippetChunk("one", "One", content))

	words := strings.Fields(joinedSpans(onlySnippet(t, service, "widget", nil)))

	require.Subset(t, words, []string{"Example:", "name:", "demo", "kind:", "widget", "After", "the", "block."})
}

func TestLabelledSearch_SnippetHighlightsStemmedMatches(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantHits []string
	}{
		{
			name:     "plain text",
			content:  "Read the configuration guide first.",
			wantHits: []string{"configuration"},
		},
		{
			name:     "inside markdown syntax",
			content:  "## Configuration\n\nSee the **configuration** reference and [configured](https://example.com/configure) values.",
			wantHits: []string{"Configuration", "configuration", "configured"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := snippetService(t, snippetChunk("one", "One", tt.content))

			spans := onlySnippet(t, service, "configuring", nil)

			requireCanonicalSpans(t, spans)
			require.Equal(t, tt.wantHits, spanTexts(spans, true))
			joined := joinedSpans(spans)
			require.NotContains(t, joined, "#")
			require.NotContains(t, joined, "*")
			require.NotContains(t, joined, "example.com")
		})
	}
}

func TestLabelledSearch_SnippetTruncatesOnWordBoundaries(t *testing.T) {
	before := strings.Repeat("lorem ipsum ", 60)
	after := strings.Repeat(" dolor sit", 60)
	vocabulary := []string{"lorem", "ipsum", "dolor", "sit", "needle"}

	tests := []struct {
		name         string
		content      string
		query        string
		labels       []string
		wantLeading  bool
		wantTrailing bool
	}{
		{name: "hit in the middle", content: before + "needle" + after, query: "needle", wantLeading: true, wantTrailing: true},
		{name: "hit at the start", content: "needle" + after, query: "needle", wantTrailing: true},
		{name: "hit at the end", content: before + "needle", query: "needle", wantLeading: true},
		{name: "label-only browse", content: before + "needle" + after, labels: []string{"snippet"}, wantTrailing: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := snippetService(t, snippetChunk("one", "One", tt.content))

			spans := onlySnippet(t, service, tt.query, tt.labels)

			requireCanonicalSpans(t, spans)
			first, last := spans[0], spans[len(spans)-1]
			joined := joinedSpans(spans)

			require.Equal(t, tt.wantLeading, strings.HasPrefix(joined, snippetEllipsis), "leading ellipsis in %q", joined)
			require.Equal(t, tt.wantTrailing, strings.HasSuffix(joined, snippetEllipsis), "trailing ellipsis in %q", joined)
			if tt.wantLeading {
				require.False(t, first.Hit, "leading ellipsis must sit in a non-hit span")
			}
			if tt.wantTrailing {
				require.False(t, last.Hit, "trailing ellipsis must sit in a non-hit span")
			}
			require.Equal(t, 0, strings.Count(strings.Trim(joined, snippetEllipsis), snippetEllipsis), "ellipsis only marks cut ends")

			body := strings.Trim(joined, snippetEllipsis)
			words := strings.Fields(body)
			require.NotEmpty(t, words)
			for _, word := range words {
				require.Contains(t, vocabulary, word, "snippet %q was cut mid-word", joined)
			}
			require.Less(t, utf8.RuneCountInString(joined), utf8.RuneCountInString(tt.content), "snippet was not truncated")
			if tt.query != "" {
				require.Equal(t, []string{"needle"}, spanTexts(spans, true))
			}
		})
	}
}

func TestLabelledSearch_SnippetFallbackIsPlainText(t *testing.T) {
	content := "# Overview\n\nSee [the guide](https://example.com/guide) for **details**."
	service := snippetService(t, snippetChunk("one", "One", content))

	spans := onlySnippet(t, service, "", []string{"snippet"})

	require.Len(t, spans, 1)
	require.False(t, spans[0].Hit)
	require.Equal(t, "Overview — See the guide for details.", spans[0].Text)
}

func TestLabelledSearch_SnippetHitsCannotBeForged(t *testing.T) {
	sentinelForgery := "fake " + string(hitStart) + "forged" + string(hitEnd) + " and **real** target here."

	tests := []struct {
		name      string
		content   string
		query     string
		wantHits  []string
		wantWords []string
	}{
		{
			name:      "literal mark element",
			content:   "Literal <mark>x</mark> tag and **real** x here.",
			query:     "x",
			wantHits:  []string{"x", "x"},
			wantWords: []string{"Literal", "<mark>x</mark>", "tag", "and", "real", "x", "here."},
		},
		{
			name:      "literal mark element around a non-matching word",
			content:   "Literal <mark>forged</mark> tag and `real` target here.",
			query:     "target",
			wantHits:  []string{"target"},
			wantWords: []string{"Literal", "<mark>forged</mark>", "tag", "and", "real", "target", "here."},
		},
		{
			name:      "sentinel runes in content",
			content:   sentinelForgery,
			query:     "target",
			wantHits:  []string{"target"},
			wantWords: []string{"fake", "forged", "and", "real", "target", "here."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := snippetService(t, snippetChunk("one", "One", tt.content))

			spans := onlySnippet(t, service, tt.query, nil)

			requireCanonicalSpans(t, spans)
			require.Equal(t, tt.wantHits, spanTexts(spans, true))
			joined := joinedSpans(spans)
			require.NotContains(t, joined, string(hitStart))
			require.NotContains(t, joined, string(hitEnd))
			require.Equal(t, tt.wantWords, strings.Fields(joined), "markdown is projected away while raw inline HTML stays literal")
		})
	}
}

func TestLabelledSearch_MarkdownSnippetsKeepSearchRanking(t *testing.T) {
	chunks := []domain.Chunk{
		snippetChunk("a", "Alpha", "# Deploy\n\nUse `docker compose` to **deploy** the [stack](https://example.com)."),
		snippetChunk("b", "Beta", "| Step | Command |\n|---|---|\n| deploy | docker run |"),
		snippetChunk("c", "Gamma", "```sh\ndocker build .\n```\n\nThen deploy."),
		snippetChunk("d", "Delta", "Nothing relevant here."),
	}
	service := snippetService(t, chunks...)

	for _, query := range []string{"docker", "deploy", "docker deploy", "stack"} {
		t.Run(query, func(t *testing.T) {
			plain, err := service.Search(query, 100)
			require.NoError(t, err)

			got, err := service.LabelledSearch(query, nil, 100)
			require.NoError(t, err)
			require.Len(t, got, len(plain))
			for i := range plain {
				require.Equal(t, plain[i].ChunkID, got[i].ChunkID, "rank %d", i+1)
				require.InDelta(t, plain[i].Score, got[i].Score, 1e-9, "score at rank %d", i+1)
			}
		})
	}
}

func TestLabelledSearch_SnippetSeparatesBlocksAndTableCells(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		query    string
		want     string
		wantHits []string
	}{
		{
			name:     "heading paragraph and table",
			content:  "## What Gets Indexed\n\nEvery resource is indexed:\n\n| Field | Source |\n|---|---|\n| name | Frontmatter |\n| body | Markdown |",
			query:    "frontmatter",
			want:     "What Gets Indexed — Every resource is indexed: — Field · Source / name · Frontmatter / body · Markdown",
			wantHits: []string{"Frontmatter"},
		},
		{
			name:     "list items",
			content:  "Steps:\n\n- install widget\n- run widget",
			query:    "widget",
			want:     "Steps: — install widget — run widget",
			wantHits: []string{"widget", "widget"},
		},
		{
			name:     "code block and blockquote",
			content:  "Run:\n\n```sh\nmake widget\n```\n\n> widget note",
			query:    "widget",
			want:     "Run: — make widget — widget note",
			wantHits: []string{"widget", "widget"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := snippetService(t, snippetChunk("one", "One", tt.content))

			spans := onlySnippet(t, service, tt.query, nil)

			requireCanonicalSpans(t, spans)
			require.Equal(t, tt.want, joinedSpans(spans))
			require.Equal(t, tt.wantHits, spanTexts(spans, true))
			for _, hit := range spanTexts(spans, true) {
				require.NotContains(t, hit, "—")
				require.NotContains(t, hit, "·")
				require.NotContains(t, hit, "/")
			}
		})
	}
}
