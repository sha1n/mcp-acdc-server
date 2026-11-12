package render_test

import (
	"html"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarkdown_ImagePlaceholderTitleShowsSourceDecodedOnce(t *testing.T) {
	tests := []struct {
		name          string
		source        string
		wantTitleHTML string
	}{
		{name: "numeric reference", source: "![a](x&#46;png)", wantTitleHTML: "Image not shown: x.png"},
		{name: "entity reference", source: "![a](a&amp;b.png)", wantTitleHTML: "Image not shown: a&amp;b.png"},
		{name: "entity spelling of an entity", source: "![a](a&amp;amp;b.png)", wantTitleHTML: "Image not shown: a&amp;amp;b.png"},
		{name: "backslash escape", source: `![a](x\_y.png)`, wantTitleHTML: "Image not shown: x_y.png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rendered := renderMarkdown(t, tt.source, newFakeResolver(nil), false)

			match := imagePattern.FindStringSubmatch(rendered)
			require.NotNil(t, match, rendered)
			require.Equal(t, tt.wantTitleHTML, match[1])
		})
	}
}

// The browser-visible fragment is the href attribute after one HTML decode;
// it must equal the authored fragment after markdown's single decode.
func TestMarkdown_ResolvedLinkFragmentIsDecodedOnce(t *testing.T) {
	const documentURL = "/ui/doc?uri=acdc%3A%2F%2Fdocs%2Fother"
	tests := []struct {
		name         string
		source       string
		wantFragment string
	}{
		{name: "entity reference", source: "[x](other.md#a&amp;b)", wantFragment: "a&b"},
		{name: "entity spelling of an entity", source: "[x](other.md#a&amp;amp;b)", wantFragment: "a&amp;b"},
		{name: "numeric reference spelling of an entity", source: "[x](other.md#a&#38;amp;b)", wantFragment: "a&amp;b"},
		{name: "percent-encoded space stays encoded once", source: "[x](other.md#sec%20one)", wantFragment: "sec%20one"},
		{name: "literal space in angle destination", source: "[x](<other.md#sec one>)", wantFragment: "sec%20one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rendered := renderMarkdown(t, tt.source, newFakeResolver(map[string]string{"other.md": "acdc://docs/other"}), false)

			requireHref(t, singleAnchor(t, rendered), documentURL+"#"+tt.wantFragment)
			require.Contains(t, rendered, `href="`+html.EscapeString(documentURL+"#"+tt.wantFragment)+`"`)
		})
	}
}
