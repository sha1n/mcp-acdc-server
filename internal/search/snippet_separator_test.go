package search

import (
	"fmt"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func manyShortBlocks(count, hitAt int) string {
	var b strings.Builder
	for i := range count {
		word := fmt.Sprintf("filler%d", i)
		if i == hitAt {
			word = "needle"
		}
		fmt.Fprintf(&b, "# H%d\n\nItem %s.\n\n", i, word)
	}
	return b.String()
}

func manyTableCells(rows, hitAt int) string {
	var b strings.Builder
	b.WriteString("| a | b | c |\n| --- | --- | --- |\n")
	for i := range rows {
		cell := fmt.Sprintf("v%d", i)
		if i == hitAt {
			cell = "needle"
		}
		fmt.Fprintf(&b, "| x%d | %s | y%d |\n", i, cell, i)
	}
	return b.String()
}

func requireNoEdgeSeparator(t *testing.T, snippet string) {
	t.Helper()
	body := strings.TrimSuffix(strings.TrimPrefix(snippet, snippetEllipsis), snippetEllipsis)
	require.NotEmpty(t, body)
	first, _ := utf8.DecodeRuneInString(body)
	last, _ := utf8.DecodeLastRuneInString(body)
	require.False(t, unicode.IsSpace(first) || unicode.IsSpace(last), "snippet %q has a whitespace edge", snippet)
	for _, mark := range []rune{'—', '·', '/'} {
		require.NotEqual(t, mark, first, "snippet %q starts with a separator", snippet)
		require.NotEqual(t, mark, last, "snippet %q ends with a separator", snippet)
	}
}

func TestLabelledSearch_SnippetNeverEdgesOnSeparator(t *testing.T) {
	const items = 60
	for _, hitAt := range []int{0, 1, 3, 7, 15, 30, 45, 58, 59} {
		for kind, content := range map[string]string{
			"blocks": manyShortBlocks(items, hitAt),
			"cells":  manyTableCells(items, hitAt),
		} {
			t.Run(fmt.Sprintf("%s hit at %d", kind, hitAt), func(t *testing.T) {
				service := snippetService(t, snippetChunk("doc", "Doc", content))
				spans := onlySnippet(t, service, "needle", nil)
				require.Equal(t, []string{"needle"}, spanTexts(spans, true))
				requireNoEdgeSeparator(t, joinedSpans(spans))
			})
		}
	}
}
