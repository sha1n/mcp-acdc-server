package content

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlainText_LocateMapsAutoLinkLabels(t *testing.T) {
	tests := []struct {
		name       string
		markdown   string
		wantText   string
		source     string
		occurrence int
	}{
		{name: "angle autolink whole label", markdown: "Visit <https://x.io/a> today.", wantText: "Visit https://x.io/a today.", source: "https://x.io/a"},
		{name: "angle autolink inner part", markdown: "Visit <https://x.io/a> today.", wantText: "Visit https://x.io/a today.", source: "x.io"},
		{name: "angle email autolink", markdown: "Mail <ops@x.io> now.", wantText: "Mail ops@x.io now.", source: "ops"},
		{name: "bare url", markdown: "Visit https://x.io/a today.", wantText: "Visit https://x.io/a today.", source: "https://x.io/a"},
		{name: "bare url inner part", markdown: "Visit https://x.io/a today.", wantText: "Visit https://x.io/a today.", source: "x.io/a"},
		{name: "bare www", markdown: "Visit www.x.io today.", wantText: "Visit www.x.io today.", source: "www.x.io"},
		{name: "bare url at line head", markdown: "https://x.io/a is home.", wantText: "https://x.io/a is home.", source: "x.io"},
		{name: "bare url with trailing punctuation", markdown: "See https://x.io/a.", wantText: "See https://x.io/a.", source: "https://x.io/a"},
		{name: "inside emphasis", markdown: "Go *https://x.io/a* now.", wantText: "Go https://x.io/a now.", source: "x.io"},
		{name: "inside strong www", markdown: "Go **www.x.io** now.", wantText: "Go www.x.io now.", source: "www.x.io"},
		{name: "inside parentheses", markdown: "Home (www.x.io/(a)) here.", wantText: "Home (www.x.io/(a)) here.", source: "www.x.io/(a)"},
		{name: "inside table cell", markdown: "| a | b |\n|---|---|\n| https://x.io/a | <https://x.io/b> |", wantText: "a · b / https://x.io/a · https://x.io/b", source: "x.io/b"},
		{name: "nested blockquote", markdown: "> https://x.io/a\n> > <https://x.io/b>", wantText: "https://x.io/a — https://x.io/b", source: "x.io/b"},
		{name: "nested list", markdown: "- item https://x.io/a\n  - nested www.x.io", wantText: "item https://x.io/a — nested www.x.io", source: "www.x.io"},
		{name: "after code span with same host", markdown: "`https://x.io` https://x.io/a", wantText: "https://x.io https://x.io/a", source: "x.io/a"},
		{name: "repeated label second occurrence", markdown: "x https://x.io/a and https://x.io/a end", wantText: "x https://x.io/a and https://x.io/a end", source: "https://x.io/a", occurrence: 2},
		{name: "repeated label third occurrence mixed kinds", markdown: "<https://x.io/a> https://x.io/a <https://x.io/a>", wantText: "https://x.io/a https://x.io/a https://x.io/a", source: "https://x.io/a", occurrence: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projection := ProjectPlainText(tt.markdown)
			require.Equal(t, tt.wantText, projection.Text)

			occurrence := max(tt.occurrence, 1)
			start := nthIndex(t, tt.markdown, tt.source, occurrence)
			from, to, ok := projection.Locate(start, start+len(tt.source))

			require.True(t, ok, "locating %q in projection %q", tt.source, projection.Text)
			require.Equal(t, tt.source, projection.Text[from:to])
			require.Equal(t, nthIndex(t, projection.Text, tt.source, occurrence), from, "mapped onto the wrong occurrence")
		})
	}
}

func TestPlainText_LocateRejectsAutoLinkDelimiters(t *testing.T) {
	markdown := "Visit <https://x.io/a> today."
	projection := ProjectPlainText(markdown)

	for _, source := range []string{"<", ">", "<https", "x.io/a>"} {
		t.Run(source, func(t *testing.T) {
			start := strings.Index(markdown, source)
			_, _, ok := projection.Locate(start, start+len(source))
			require.False(t, ok)
		})
	}
}

func TestProjectPlainText_DoesNotLinkifyEscapedOrLinkTextURLs(t *testing.T) {
	tests := []struct {
		name     string
		markdown string
		want     string
		source   string
	}{
		{name: "escaped angle bracket", markdown: "\\<https://x.io/a>", want: "<https://x.io/a>", source: "x.io"},
		{name: "url inside link text", markdown: "[see https://x.io/a](https://y.io)", want: "see https://x.io/a", source: "x.io"},
		{name: "angle autolink inside link text", markdown: "[<https://x.io/a>](https://y.io)", want: "https://x.io/a", source: "x.io"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projection := ProjectPlainText(tt.markdown)
			require.Equal(t, tt.want, projection.Text)

			start := strings.Index(tt.markdown, tt.source)
			from, to, ok := projection.Locate(start, start+len(tt.source))
			require.True(t, ok)
			require.Equal(t, tt.source, projection.Text[from:to])
		})
	}
}
