package content

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type excerptArgs struct {
	start, end, keepStart, keepEnd int
}

func noKeep(start, end int) excerptArgs {
	return excerptArgs{start: start, end: end}
}

func requireExcerpt(t *testing.T, text string, args excerptArgs) Excerpt {
	t.Helper()
	return requirePlainTextExcerpt(t, PlainText{Text: text}, args)
}

func requirePlainTextExcerpt(t *testing.T, plain PlainText, args excerptArgs) Excerpt {
	t.Helper()
	text := plain.Text
	var got Excerpt
	require.NotPanics(t, func() {
		got = plain.Excerpt(args.start, args.end, args.keepStart, args.keepEnd)
	})
	require.GreaterOrEqual(t, got.Start, 0)
	require.LessOrEqual(t, got.Start, got.End)
	require.LessOrEqual(t, got.End, len(text))
	assert.Equal(t, strings.TrimSpace(text[:got.Start]) != "", got.CutBefore, "CutBefore")
	assert.Equal(t, strings.TrimSpace(text[got.End:]) != "", got.CutAfter, "CutAfter")
	return got
}

// "alpha beta gamma": alpha [0,5) beta [6,10) gamma [11,16).
// "alpha beta gamma delta": additionally delta [17,22).
// "ünïcode wörd façade": ünïcode [0,9) wörd [10,15) façade [16,23).
func TestPlainTextExcerpt_Boundaries(t *testing.T) {
	const abg = "alpha beta gamma"
	const abgd = "alpha beta gamma delta"
	const multibyte = "ünïcode wörd façade"
	xs := strings.Repeat("x", 300)

	tests := []struct {
		name string
		text string
		args excerptArgs
		want Excerpt
	}{
		{
			name: "whole text window is kept whole",
			text: abg, args: noKeep(0, 16),
			want: Excerpt{Start: 0, End: 16},
		},
		{
			name: "mid-word edges align inward to the whole word beta",
			text: abg, args: noKeep(2, 12),
			want: Excerpt{Start: 6, End: 10, CutBefore: true, CutAfter: true},
		},
		{
			name: "window 2-8 holds no whole word so it falls back to the hard cut pha be rather than the partial be",
			text: abg, args: noKeep(2, 8),
			want: Excerpt{Start: 2, End: 8, CutBefore: true, CutAfter: true},
		},
		{
			name: "window inside a single word is a non-empty hard cut",
			text: "alphabet", args: noKeep(1, 4),
			want: Excerpt{Start: 1, End: 4, CutBefore: true, CutAfter: true},
		},
		{
			name: "unbroken run longer than the window is a hard cut",
			text: xs, args: noKeep(0, 200),
			want: Excerpt{Start: 0, End: 200, CutAfter: true},
		},
		{
			name: "keep inside the window does not stop alignment outside it",
			text: abgd, args: excerptArgs{start: 2, end: 20, keepStart: 6, keepEnd: 10},
			want: Excerpt{Start: 6, End: 16, CutBefore: true, CutAfter: true},
		},
		{
			name: "keep equal to the window is kept exactly",
			text: abgd, args: excerptArgs{start: 6, end: 10, keepStart: 6, keepEnd: 10},
			want: Excerpt{Start: 6, End: 10, CutBefore: true, CutAfter: true},
		},
		{
			name: "keep exceeding the window expands the window to cover it",
			text: abgd, args: excerptArgs{start: 8, end: 9, keepStart: 6, keepEnd: 16},
			want: Excerpt{Start: 6, End: 16, CutBefore: true, CutAfter: true},
		},
		{
			name: "keep reaching past the window end expands only the end",
			text: abgd, args: excerptArgs{start: 6, end: 12, keepStart: 11, keepEnd: 22},
			want: Excerpt{Start: 6, End: 22, CutBefore: true},
		},
		{
			name: "mid-word keep range is never cut into",
			text: abgd, args: excerptArgs{start: 0, end: 22, keepStart: 7, keepEnd: 9},
			want: Excerpt{Start: 0, End: 22},
		},
		{
			name: "negative start clamps to zero",
			text: abg, args: noKeep(-5, 10),
			want: Excerpt{Start: 0, End: 10, CutAfter: true},
		},
		{
			name: "end past the text clamps to its length",
			text: abg, args: noKeep(6, 100),
			want: Excerpt{Start: 6, End: 16, CutBefore: true},
		},
		{
			name: "window beyond the text clamps to an empty tail",
			text: abg, args: noKeep(50, 100),
			want: Excerpt{Start: 16, End: 16, CutBefore: true},
		},
		{
			name: "reversed keep is no keep",
			text: abg, args: excerptArgs{start: 2, end: 12, keepStart: 10, keepEnd: 6},
			want: Excerpt{Start: 6, End: 10, CutBefore: true, CutAfter: true},
		},
		{
			name: "out of range keep clamps to the whole text",
			text: abg, args: excerptArgs{start: 6, end: 10, keepStart: -3, keepEnd: 100},
			want: Excerpt{Start: 0, End: 16},
		},
		{
			name: "multibyte mid-word edges align to wörd",
			text: multibyte, args: noKeep(3, 20),
			want: Excerpt{Start: 10, End: 15, CutBefore: true, CutAfter: true},
		},
		{
			name: "start inside a multibyte rune aligns forward to the next word",
			text: multibyte, args: noKeep(1, 23),
			want: Excerpt{Start: 10, End: 23, CutBefore: true},
		},
		{
			name: "multibyte whole text",
			text: multibyte, args: noKeep(0, 23),
			want: Excerpt{Start: 0, End: 23},
		},
		{
			name: "empty text",
			text: "", args: noKeep(0, 0),
			want: Excerpt{},
		},
		{
			name: "empty text with out of range arguments",
			text: "", args: excerptArgs{start: -1, end: 5, keepStart: 2, keepEnd: 3},
			want: Excerpt{},
		},
		{
			name: "edge spaces are trimmed",
			text: abg, args: noKeep(5, 11),
			want: Excerpt{Start: 6, End: 10, CutBefore: true, CutAfter: true},
		},
		{
			name: "separator inside the keep range is kept",
			text: "alpha — beta", args: excerptArgs{start: 5, end: 14, keepStart: 6, keepEnd: 9},
			want: Excerpt{Start: 6, End: 14, CutBefore: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, requireExcerpt(t, tt.text, tt.args))
		})
	}
}

func TestPlainTextExcerpt_ReversedWindowIsEmpty(t *testing.T) {
	got := requireExcerpt(t, "alpha beta gamma", noKeep(10, 6))
	require.Equal(t, got.Start, got.End)
}

func TestPlainTextExcerpt_NeverPanics(t *testing.T) {
	texts := []string{"", "a", "alpha beta", "ünïcode wörd", "alpha — beta · gamma / delta", strings.Repeat("x", 30)}
	values := []int{-7, -1, 0, 1, 2, 3, 5, 6, 9, 13, 100}
	for _, text := range texts {
		for _, start := range values {
			for _, end := range values {
				for _, keepStart := range []int{-1, 0, 2, 6} {
					for _, keepEnd := range []int{-1, 0, 4, 9, 100} {
						requireExcerpt(t, text, excerptArgs{start, end, keepStart, keepEnd})
					}
				}
			}
		}
	}
}

// separatorMarkdown projects to its separator joining the given words, so the
// separators are recorded as inserted by the projection rather than authored.
var separatorMarkdown = map[string]func(words ...string) string{
	" — ": func(words ...string) string { return strings.Join(words, "\n\n") },
	" · ": func(words ...string) string {
		return "| " + strings.Join(words, " | ") + " |\n" + strings.Repeat("|---", len(words)) + "|"
	},
	" / ": func(words ...string) string {
		return "| " + words[0] + " |\n|---|\n| " + strings.Join(words[1:], " |\n| ") + " |"
	},
}

func projectedSeparatorText(t *testing.T, sep string, words ...string) PlainText {
	t.Helper()
	plain := ProjectPlainText(separatorMarkdown[sep](words...))
	require.Equal(t, strings.Join(words, sep), plain.Text)
	return plain
}

func TestPlainTextExcerpt_TrimsSeparatorsAtEdges(t *testing.T) {
	for sep := range separatorMarkdown {
		text := "alpha" + sep + "beta" + sep + "gamma"
		beta := strings.Index(text, "beta")
		afterBeta := beta + len("beta")
		mark := len("alpha ")
		trailingMarkEnd := afterBeta + len(strings.TrimRight(sep, " "))

		tests := []struct {
			name string
			args excerptArgs
			want Excerpt
		}{
			{
				name: "both edges",
				args: noKeep(mark, trailingMarkEnd),
				want: Excerpt{Start: beta, End: afterBeta, CutBefore: true, CutAfter: true},
			},
			{
				name: "leading edge",
				args: noKeep(mark, afterBeta),
				want: Excerpt{Start: beta, End: afterBeta, CutBefore: true, CutAfter: true},
			},
			{
				name: "trailing edge",
				args: noKeep(beta, trailingMarkEnd),
				want: Excerpt{Start: beta, End: afterBeta, CutBefore: true, CutAfter: true},
			},
			{
				name: "leading edge with its space",
				args: noKeep(len("alpha"), afterBeta),
				want: Excerpt{Start: beta, End: afterBeta, CutBefore: true, CutAfter: true},
			},
		}
		for _, tt := range tests {
			t.Run(strings.TrimSpace(sep)+" "+tt.name, func(t *testing.T) {
				plain := projectedSeparatorText(t, sep, "alpha", "beta", "gamma")
				require.Equal(t, tt.want, requirePlainTextExcerpt(t, plain, tt.args))
			})
		}
	}
}

func TestPlainTextExcerpt_SeparatorOnlyWindowIsEmpty(t *testing.T) {
	for sep := range separatorMarkdown {
		t.Run(strings.TrimSpace(sep), func(t *testing.T) {
			plain := projectedSeparatorText(t, sep, "alpha", "beta")
			got := requirePlainTextExcerpt(t, plain, noKeep(len("alpha"), len("alpha"+sep)))
			require.Equal(t, got.Start, got.End, "excerpt %q", plain.Text[got.Start:got.End])
		})
	}
}
