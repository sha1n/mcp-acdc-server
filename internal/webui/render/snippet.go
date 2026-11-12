package render

import (
	"html"
	"strings"

	"github.com/sha1n/mcp-acdc-server/internal/search"
)

// Snippet renders spans as escaped HTML with hits wrapped in <mark>.
func Snippet(spans []search.SnippetSpan) string {
	var out strings.Builder
	for _, span := range spans {
		escaped := html.EscapeString(span.Text)
		if span.Hit {
			out.WriteString("<mark>" + escaped + "</mark>")
			continue
		}
		out.WriteString(escaped)
	}
	return out.String()
}
