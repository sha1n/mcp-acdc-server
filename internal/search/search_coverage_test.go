package search

import (
	"errors"
	"strings"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/stretchr/testify/require"
)

func TestLabels_ReportsSearchFailure(t *testing.T) {
	backing, err := bleve.NewMemOnly(buildMapping())
	require.NoError(t, err)
	service := NewService(testSettings())
	defer service.Close()
	service.index = &failingSearchIndex{backing: backing, searchErr: errors.New("boom")}

	for _, prefix := range []string{"", "doc"} {
		counts, err := service.Labels(prefix, 10)
		require.ErrorContains(t, err, "boom")
		require.Nil(t, counts)
	}
}

func TestLabelledSearch_SnippetContentSentinelsCannotForgeHit(t *testing.T) {
	forged := string(rune(hitStart)) + "forged" + string(rune(hitEnd))
	service := snippetService(t, snippetChunk("sentinel", "Sentinel",
		"intro "+forged+" middle alpha "+string(rune(hitEnd))+"tail"+string(rune(hitStart))))

	spans := onlySnippet(t, service, "alpha", nil)

	requireCanonicalSpans(t, spans)
	require.Equal(t, []string{"alpha"}, spanTexts(spans, true))
	joined := joinedSpans(spans)
	require.NotContains(t, joined, string(rune(hitStart)))
	require.NotContains(t, joined, string(rune(hitEnd)))
	require.Equal(t, "intro forged middle alpha tail", joined)
}

func TestLabelledSearch_SnippetEmptyContentAndTitleYieldsNoSpans(t *testing.T) {
	service := snippetService(t, snippetChunk("blank", "", ""))

	spans := onlySnippet(t, service, "", []string{"snippet"})

	require.Equal(t, []SnippetSpan{}, spans)
}

func TestLabelledSearch_SnippetMergesAdjacentHits(t *testing.T) {
	service := snippetService(t, snippetChunk("cjk", "CJK", "before 日本 after"))

	spans := onlySnippet(t, service, "日本", nil)

	requireCanonicalSpans(t, spans)
	require.Equal(t, []string{"日本"}, spanTexts(spans, true))
}

func TestLabelledSearch_SnippetOmitsHitsBeyondFragment(t *testing.T) {
	content := "alpha " + strings.Repeat("filler ", 200) + "alpha"
	service := snippetService(t, snippetChunk("long", "Long", content))

	spans := onlySnippet(t, service, "alpha", nil)

	requireCanonicalSpans(t, spans)
	require.Len(t, spanTexts(spans, true), 1)
	require.Less(t, len(joinedSpans(spans)), len(content))
}
