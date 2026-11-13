package search

import (
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/stretchr/testify/require"
)

func snippetService(t *testing.T, chunks ...domain.Chunk) *Service {
	t.Helper()
	return labelServiceWith(t, testSettings(), chunks)
}

func snippetChunk(id, title, content string) domain.Chunk {
	return domain.Chunk{
		ID: id, SourceURI: "acdc://" + id, ChunkURI: "acdc://" + id,
		SourceTitle: title, Content: content, Keywords: []string{"snippet"},
	}
}

func onlySnippet(t *testing.T, service *Service, query string, labels []string) []SnippetSpan {
	t.Helper()
	results, err := service.LabelledSearch(query, labels, 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.NotNil(t, results[0].Snippet)
	return results[0].Snippet
}

func requireCanonicalSpans(t *testing.T, spans []SnippetSpan) {
	t.Helper()
	for i, span := range spans {
		require.NotEmpty(t, span.Text, "span %d is empty", i)
		if i > 0 {
			require.NotEqual(t, spans[i-1].Hit, span.Hit, "spans %d and %d share Hit=%v and were not merged", i-1, i, span.Hit)
		}
	}
}

func spanTexts(spans []SnippetSpan, hit bool) []string {
	var texts []string
	for _, span := range spans {
		if span.Hit == hit {
			texts = append(texts, span.Text)
		}
	}
	return texts
}

func joinedSpans(spans []SnippetSpan) string {
	var b strings.Builder
	for _, span := range spans {
		b.WriteString(span.Text)
	}
	return b.String()
}

func TestLabelledSearch_SnippetMarksQueryHits(t *testing.T) {
	content := "Use docker compose to deploy the stack."
	service := snippetService(t, snippetChunk("one", "One", content))

	spans := onlySnippet(t, service, "compose", nil)

	requireCanonicalSpans(t, spans)
	require.Equal(t, []string{"compose"}, spanTexts(spans, true))
	require.Equal(t, content, joinedSpans(spans), "a short chunk's fragment is the whole content, unescaped")
}

func TestLabelledSearch_SnippetKeepsRepeatedHitsInOriginalCase(t *testing.T) {
	content := "Intro text. Compose compose then more prose."
	service := snippetService(t, snippetChunk("one", "One", content))

	spans := onlySnippet(t, service, "compose", nil)

	requireCanonicalSpans(t, spans)
	require.Equal(t, content, joinedSpans(spans))
	require.Equal(t, []string{"Compose", "compose"}, spanTexts(spans, true))
}

func TestLabelledSearch_SnippetTreatsLiteralMarkupAsText(t *testing.T) {
	content := "Literal <mark>x</mark> tag and real x here."
	service := snippetService(t, snippetChunk("one", "One", content))

	spans := onlySnippet(t, service, "x", nil)

	requireCanonicalSpans(t, spans)
	require.Equal(t, []string{"x", "x"}, spanTexts(spans, true), "only the two real term occurrences are hits")
	plain := strings.Join(spanTexts(spans, false), "")
	require.Contains(t, plain, "<mark>")
	require.Contains(t, plain, "</mark>")
	require.Equal(t, content, joinedSpans(spans), "literal markup survives verbatim and unescaped")
}

func TestLabelledSearch_SnippetFallsBackWithoutContentFragment(t *testing.T) {
	long := strings.Repeat("ünïcode wörd ", 30)
	longPrefix := strings.TrimSpace(strings.Repeat("ünïcode wörd ", 15)) + "…"

	tests := []struct {
		name   string
		chunk  domain.Chunk
		query  string
		labels []string
		want   string
	}{
		{
			name:   "label-only browse uses content",
			chunk:  snippetChunk("one", "Title", "Short body."),
			labels: []string{"snippet"},
			want:   "Short body.",
		},
		{
			name:   "label-only browse truncates on a word boundary within 200 runes",
			chunk:  snippetChunk("one", "Title", long),
			labels: []string{"snippet"},
			want:   longPrefix,
		},
		{
			name:  "match all uses content",
			chunk: snippetChunk("one", "Title", "Short body."),
			query: "*",
			want:  "Short body.",
		},
		{
			name:  "query hitting only the title uses content",
			chunk: snippetChunk("one", "Zephyr", "Plain body text."),
			query: "zephyr",
			want:  "Plain body text.",
		},
		{
			name:   "empty content uses title",
			chunk:  snippetChunk("one", "Only Title", ""),
			labels: []string{"snippet"},
			want:   "Only Title",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := snippetService(t, tt.chunk)

			spans := onlySnippet(t, service, tt.query, tt.labels)

			require.Equal(t, []SnippetSpan{{Text: tt.want, Hit: false}}, spans)
		})
	}
}
