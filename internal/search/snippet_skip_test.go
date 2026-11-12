package search

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLabelledSearch_SnippetSkipsHitsBeforeChosenFragment(t *testing.T) {
	content := "alpha " + strings.Repeat("filler ", 200) + "alpha beta alpha beta"
	service := snippetService(t, snippetChunk("skip", "Skip", content))

	spans := onlySnippet(t, service, "alpha beta", nil)

	requireCanonicalSpans(t, spans)
	joined := joinedSpans(spans)
	require.Contains(t, content, strings.Trim(joined, "…"), "snippet is not a slice of the content")
	require.NotContains(t, joined, string(rune(hitStart)))
	require.NotContains(t, joined, string(rune(hitEnd)))
	require.True(t, strings.HasSuffix(joined, "alpha beta alpha beta"), "chosen fragment %q should end with the dense hit run", joined)
	require.Equal(t, []string{"alpha", "beta", "alpha", "beta"}, spanTexts(spans, true))
	for _, span := range spans {
		if !span.Hit {
			require.NotContains(t, strings.Fields(span.Text), "alpha")
			require.NotContains(t, strings.Fields(span.Text), "beta")
		}
	}
}
