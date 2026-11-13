package render_test

import (
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/webui/render"
	"github.com/stretchr/testify/require"
)

func TestDocumentURL_EscapesURIAndAppendsFragment(t *testing.T) {
	cases := []struct {
		name      string
		sourceURI string
		fragment  string
		want      string
	}{
		{name: "no fragment", sourceURI: "acdc://docs/guide", want: "/ui/doc?uri=acdc%3A%2F%2Fdocs%2Fguide"},
		{name: "with fragment", sourceURI: "acdc://docs/guide", fragment: "install", want: "/ui/doc?uri=acdc%3A%2F%2Fdocs%2Fguide#install"},
		{name: "query characters in uri", sourceURI: "acdc://x?y=1&z#w", fragment: "f", want: "/ui/doc?uri=acdc%3A%2F%2Fx%3Fy%3D1%26z%23w#f"},
		{name: "spaces in uri", sourceURI: "acdc://docs/a b", want: "/ui/doc?uri=acdc%3A%2F%2Fdocs%2Fa+b"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, render.DocumentURL(tc.sourceURI, tc.fragment))
		})
	}
}

func TestResultDocumentURL_AnchorsAtChunkHeading(t *testing.T) {
	const source = "acdc://docs/guide"
	const base = "/ui/doc?uri=acdc%3A%2F%2Fdocs%2Fguide"

	cases := []struct {
		name     string
		chunkURI string
		want     string
	}{
		{name: "section chunk", chunkURI: source + "#install", want: base + "#install"},
		{name: "split part anchors at section heading", chunkURI: source + "#install~2", want: base + "#install"},
		{name: "split part of suffixed section", chunkURI: source + "#install-1~3", want: base + "#install-1"},
		{name: "heading named document", chunkURI: source + "#document-1", want: base + "#document-1"},
		{name: "preamble lands at top", chunkURI: source + "#document", want: base},
		{name: "split preamble lands at top", chunkURI: source + "#document~2", want: base},
		{name: "chunk uri without fragment", chunkURI: source, want: base},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, render.ResultDocumentURL(source, tc.chunkURI))
		})
	}
}
