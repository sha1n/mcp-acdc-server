package search

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLabelledSearch_SnippetKeepsAuthoredSeparatorMark(t *testing.T) {
	service := snippetService(t, snippetChunk("slash", "Slash", "`/`"))
	spans := onlySnippet(t, service, "", []string{"snippet"})
	require.Equal(t, "/", joinedSpans(spans))
}
