package render_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarkdown_ImagePlaceholderShowsAltAsPlainText(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		wantAlt string
	}{
		{name: "backslash escape", source: `![a \* b](x.png)`, wantAlt: "a * b"},
		{name: "entity reference", source: `![fish &amp; chips](x.png)`, wantAlt: "fish &amp; chips"},
		{name: "numeric reference", source: `![caf&#233;](x.png)`, wantAlt: "café"},
		{name: "inline markup", source: `![**bold** _it_ ` + "`code`" + ` [link](y.md)](x.png)`, wantAlt: "bold it code link"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rendered := renderMarkdown(t, tt.source, newFakeResolver(nil), false)

			match := imagePattern.FindStringSubmatch(rendered)
			require.NotNil(t, match, rendered)
			require.Equal(t, tt.wantAlt, match[2])
		})
	}
}
