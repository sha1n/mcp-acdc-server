package render_test

import (
	"html"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sha1n/mcp-acdc-server/internal/webui/render"
)

func TestMarkdown_EntityEncodedRelativeLinksResolveDecoded(t *testing.T) {
	cases := []struct {
		name     string
		markdown string
		target   string
		href     string
	}{
		{name: "decimal reference", markdown: "[x](a&#46;md)", target: "a.md", href: render.DocumentURL("acdc://a", "")},
		{name: "hex reference", markdown: "[x](a&#x2E;md)", target: "a.md", href: render.DocumentURL("acdc://a", "")},
		{name: "named reference", markdown: "[x](a&period;md)", target: "a.md", href: render.DocumentURL("acdc://a", "")},
		{name: "encoded fragment separator", markdown: "[x](a.md&#35;sec)", target: "a.md", href: render.DocumentURL("acdc://a", "sec")},
		{name: "encoded percent escape", markdown: "[x](a&#37;20b.md)", target: "a b.md", href: render.DocumentURL("acdc://ab", "")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := newFakeResolver(map[string]string{"a.md": "acdc://a", "a b.md": "acdc://ab"})
			rendered := renderMarkdown(t, tc.markdown, resolver, false)

			a := singleAnchor(t, rendered)
			requireHref(t, a, tc.href)
			requireInternal(t, a)
			require.Equal(t, []resolveCall{{fromPath: fromPath, target: tc.target}}, resolver.recordedCalls())
			require.NotContains(t, rendered, "broken-link")
		})
	}
}

func TestMarkdown_EntityEncodedScriptSchemesNeverProduceHref(t *testing.T) {
	cases := []struct {
		name     string
		markdown string
		title    string
	}{
		{name: "decimal colon", markdown: "[x](javascript&#58;alert(1))", title: "Broken link: javascript:alert(1)"},
		{name: "decimal leading letter", markdown: "[x](&#106;avascript:alert(1))", title: "Broken link: javascript:alert(1)"},
		{name: "hex colon", markdown: "[x](javascript&#x3A;alert(1))", title: "Broken link: javascript:alert(1)"},
		{name: "hex leading letter", markdown: "[x](&#x6A;avascript:alert(1))", title: "Broken link: javascript:alert(1)"},
		{name: "named colon", markdown: "[x](javascript&colon;alert(1))", title: "Broken link: javascript:alert(1)"},
		{name: "fully encoded", markdown: "[x](&#106;&#97;&#118;&#97;&#115;&#99;&#114;&#105;&#112;&#116;&#58;alert(1))", title: "Broken link: javascript:alert(1)"},
		{name: "data scheme", markdown: "[x](data&#58;text/html,hi)", title: "Broken link: data:text/html,hi"},
		{name: "encoded absolute path", markdown: "[x](&#47;etc/passwd)", title: "Broken link: /etc/passwd"},
		{name: "encoded protocol-relative", markdown: "[x](&#47;&#47;evil.example/x)", title: "Broken link: //evil.example/x"},
		{name: "encoded query", markdown: "[x](page.md&#63;x=1)", title: "Broken link: page.md?x=1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := newFakeResolver(map[string]string{
				"/etc/passwd":      "acdc://leak",
				"//evil.example/x": "acdc://leak",
				"page.md?x=1":      "acdc://leak",
				"page.md":          "acdc://leak",
			})
			rendered := renderMarkdown(t, tc.markdown, resolver, false)

			require.Empty(t, anchors(rendered), "encoded link must not be navigable: %s", rendered)
			require.NotContains(t, rendered, "href=")
			require.NotContains(t, rendered, "leak")
			match := brokenPattern.FindStringSubmatch(rendered)
			require.NotNil(t, match, "expected broken-link span in %s", rendered)
			require.Equal(t, tc.title, html.UnescapeString(match[1]))
			require.Equal(t, "x", match[2])
		})
	}
}

func TestMarkdown_EntityEncodedHTTPSchemeIsExternal(t *testing.T) {
	resolver := newFakeResolver(nil)
	rendered := renderMarkdown(t, "[x](https&#58;//example.com)", resolver, false)

	a := singleAnchor(t, rendered)
	requireHref(t, a, "https://example.com")
	target, _ := attr(a.attrs, "target")
	rel, _ := attr(a.attrs, "rel")
	require.Equal(t, "_blank", target)
	require.Equal(t, "noopener noreferrer", rel)
	require.Empty(t, resolver.recordedCalls())
}

func TestMarkdown_BrokenLinkTitleShowsDecodedTargetEscapedOnce(t *testing.T) {
	cases := []struct {
		name     string
		markdown string
		rawTitle string
	}{
		{name: "ampersand reference", markdown: "[x](a&amp;b.md)", rawTitle: "Broken link: a&amp;b.md"},
		{name: "less-than reference", markdown: "[x](a&lt;b.md)", rawTitle: "Broken link: a&lt;b.md"},
		{name: "colon reference", markdown: "[x](javascript&#58;alert(1))", rawTitle: "Broken link: javascript:alert(1)"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rendered := renderMarkdown(t, tc.markdown, newFakeResolver(nil), false)

			match := brokenPattern.FindStringSubmatch(rendered)
			require.NotNil(t, match, "expected broken-link span in %s", rendered)
			require.Equal(t, tc.rawTitle, match[1])
		})
	}
}
