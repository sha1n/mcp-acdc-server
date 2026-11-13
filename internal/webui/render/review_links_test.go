package render_test

import (
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/webui/render"
	"github.com/stretchr/testify/require"
)

func TestMarkdown_PercentEncodedColonTargetResolvesAsRelativePath(t *testing.T) {
	cases := []struct {
		name     string
		markdown string
		call     resolveCall
		href     string
	}{
		{
			name:     "bare relative path",
			markdown: "[v2](notes%3Av2.md)",
			call:     resolveCall{fromPath: fromPath, target: "notes:v2.md"},
			href:     "/ui/doc?uri=acdc%3A%2F%2Fguides%2Fnotes-v2",
		},
		{
			name:     "dot-relative path",
			markdown: "[v2](./notes%3Av2.md)",
			call:     resolveCall{fromPath: fromPath, target: "./notes:v2.md"},
			href:     "/ui/doc?uri=acdc%3A%2F%2Fguides%2Fnotes-v2",
		},
		{
			name:     "relative path with fragment",
			markdown: "[v2](notes%3Av2.md#changes)",
			call:     resolveCall{fromPath: fromPath, target: "notes:v2.md"},
			href:     "/ui/doc?uri=acdc%3A%2F%2Fguides%2Fnotes-v2#changes",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := newFakeResolver(map[string]string{
				"notes:v2.md":   "acdc://guides/notes-v2",
				"./notes:v2.md": "acdc://guides/notes-v2",
			})
			rendered := renderMarkdown(t, tc.markdown, resolver, true)

			a := singleAnchor(t, rendered)
			requireHref(t, a, tc.href)
			requireInternal(t, a)
			require.NotContains(t, rendered, "broken-link")
			require.Equal(t, []resolveCall{tc.call}, resolver.recordedCalls())
		})
	}
}

func TestMarkdown_SchemeTargetResolvesAsURI(t *testing.T) {
	resolver := newFakeResolver(nil)
	rendered := renderMarkdown(t, "[v2](notes:v2.md)", resolver, true)

	require.Empty(t, anchors(rendered))
	require.Contains(t, rendered, "broken-link")
	require.Equal(t, []resolveCall{{target: "notes:v2.md"}}, resolver.recordedCalls(), "a literal colon makes a scheme, not a relative path")
}

func TestMarkdown_FragmentLinkQualifiedOnlyWithoutHeadingIDs(t *testing.T) {
	const sourceURI = "acdc://guides/setup"
	cases := []struct {
		name       string
		markdown   string
		headingIDs bool
		href       string
	}{
		{name: "preview", markdown: "[Install](#install)", headingIDs: false, href: render.DocumentURL(sourceURI, "install")},
		{name: "preview escapes source uri", markdown: "[Install](#install)", headingIDs: false, href: "/ui/doc?uri=acdc%3A%2F%2Fguides%2Fsetup#install"},
		{name: "preview reference-style link", markdown: "[Install][i]\n\n[i]: #install", headingIDs: false, href: "/ui/doc?uri=acdc%3A%2F%2Fguides%2Fsetup#install"},
		{name: "whole document", markdown: "[Install](#install)", headingIDs: true, href: "#install"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := newFakeResolver(nil)
			rendered := render.Markdown(tc.markdown, render.Options{
				FromPath: fromPath, SourceURI: sourceURI, HeadingIDs: tc.headingIDs, Links: resolver,
			})

			a := singleAnchor(t, rendered)
			requireHref(t, a, tc.href)
			requireInternal(t, a)
			require.Empty(t, resolver.recordedCalls())
		})
	}
}
