package content

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func projectedExcerpt(t *testing.T, markdown string, window func(text string) (int, int)) string {
	t.Helper()
	plain := ProjectPlainText(markdown)
	start, end := window(plain.Text)
	var got Excerpt
	require.NotPanics(t, func() { got = plain.Excerpt(start, end, 0, 0) })
	require.LessOrEqual(t, 0, got.Start)
	require.LessOrEqual(t, got.Start, got.End)
	require.LessOrEqual(t, got.End, len(plain.Text))
	return plain.Text[got.Start:got.End]
}

func wholeText(text string) (int, int) { return 0, len(text) }

func between(from, to string) func(string) (int, int) {
	return func(text string) (int, int) {
		return strings.Index(text, from), strings.Index(text, to)
	}
}

func TestPlainTextExcerpt_KeepsAuthoredSeparatorMarks(t *testing.T) {
	tests := []struct {
		name     string
		markdown string
		window   func(string) (int, int)
		want     string
	}{
		{name: "inline code slash alone", markdown: "`/`", window: wholeText, want: "/"},
		{name: "authored trailing slash", markdown: "alpha /", window: wholeText, want: "alpha /"},
		{name: "authored middle dots at both edges", markdown: "· edge ·", window: wholeText, want: "· edge ·"},
		{name: "authored em dashes at both edges", markdown: "— edge —", window: wholeText, want: "— edge —"},
		{
			name:     "authored slash before an inserted block separator",
			markdown: "alpha /\n\nbeta",
			window:   between("alpha", "beta"),
			want:     "alpha /",
		},
		{
			name:     "authored slash after an inserted block separator",
			markdown: "alpha\n\n/ beta",
			window: func(text string) (int, int) {
				return len("alpha"), len(text)
			},
			want: "/ beta",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, projectedExcerpt(t, tt.markdown, tt.window))
		})
	}
}

func TestPlainTextExcerpt_TrimsInsertedSeparators(t *testing.T) {
	const table = "| a | b |\n|---|---|\n| c | d |"
	tests := []struct {
		name     string
		markdown string
		window   func(string) (int, int)
		want     string
	}{
		{
			name:     "cell separators at both edges",
			markdown: table,
			window: func(text string) (int, int) {
				return len("a"), strings.Index(text, "d")
			},
			want: "b / c",
		},
		{
			name:     "row separators at both edges",
			markdown: "| a |\n|---|\n| b |\n| c |",
			window: func(text string) (int, int) {
				return len("a"), strings.Index(text, "c")
			},
			want: "b",
		},
		{
			name:     "block separator at the leading edge",
			markdown: "first\n\nsecond",
			window: func(text string) (int, int) {
				return len("first"), len(text)
			},
			want: "second",
		},
		{
			name:     "block separator at the trailing edge",
			markdown: "first\n\nsecond",
			window:   between("first", "second"),
			want:     "first",
		},
		{
			name:     "window of only a block separator",
			markdown: "first\n\nsecond",
			window: func(text string) (int, int) {
				return len("first"), strings.Index(text, "second")
			},
			want: "",
		},
		{
			name:     "window of only a cell separator",
			markdown: table,
			window: func(text string) (int, int) {
				return len("a"), strings.Index(text, "b")
			},
			want: "",
		},
		{
			name:     "window of only a row separator",
			markdown: table,
			window: func(text string) (int, int) {
				return strings.Index(text, "b") + len("b"), strings.Index(text, "c")
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, projectedExcerpt(t, tt.markdown, tt.window))
		})
	}
}
