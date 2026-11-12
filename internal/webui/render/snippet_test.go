package render_test

import (
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/sha1n/mcp-acdc-server/internal/webui/render"
	"github.com/stretchr/testify/require"
)

func TestSnippet_EscapesTextAndMarksHits(t *testing.T) {
	cases := []struct {
		name  string
		spans []search.SnippetSpan
		want  string
	}{
		{name: "no spans", spans: nil, want: ""},
		{name: "plain text", spans: []search.SnippetSpan{{Text: "plain"}}, want: "plain"},
		{
			name:  "hit between context",
			spans: []search.SnippetSpan{{Text: "run "}, {Text: "docker", Hit: true}, {Text: " now"}},
			want:  "run <mark>docker</mark> now",
		},
		{
			name:  "html special characters escaped",
			spans: []search.SnippetSpan{{Text: `a <b> & "c" 'd'`}},
			want:  "a &lt;b&gt; &amp; &#34;c&#34; &#39;d&#39;",
		},
		{
			name:  "literal mark markup in content is text",
			spans: []search.SnippetSpan{{Text: "<mark>x</mark> "}, {Text: "x", Hit: true}},
			want:  "&lt;mark&gt;x&lt;/mark&gt; <mark>x</mark>",
		},
		{
			name:  "hit text escaped inside mark",
			spans: []search.SnippetSpan{{Text: "<script>", Hit: true}},
			want:  "<mark>&lt;script&gt;</mark>",
		},
		{
			name:  "consecutive hits each wrapped",
			spans: []search.SnippetSpan{{Text: "a", Hit: true}, {Text: " "}, {Text: "b", Hit: true}},
			want:  "<mark>a</mark> <mark>b</mark>",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, render.Snippet(tc.spans))
		})
	}
}
