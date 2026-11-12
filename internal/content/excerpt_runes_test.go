package content

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireRuneSafeExcerpt(t *testing.T, text string, args excerptArgs) Excerpt {
	t.Helper()
	var got Excerpt
	require.NotPanics(t, func() {
		got = PlainText{Text: text}.Excerpt(args.start, args.end, args.keepStart, args.keepEnd)
	}, "args %+v", args)
	require.GreaterOrEqual(t, got.Start, 0, "args %+v", args)
	require.LessOrEqual(t, got.Start, got.End, "args %+v", args)
	require.LessOrEqual(t, got.End, len(text), "args %+v", args)
	assert.True(t, isRuneBoundary(text, got.Start), "Start %d splits a rune, args %+v", got.Start, args)
	assert.True(t, isRuneBoundary(text, got.End), "End %d splits a rune, args %+v", got.End, args)
	assert.True(t, utf8.ValidString(text[got.Start:got.End]), "invalid UTF-8 %q, args %+v", text[got.Start:got.End], args)
	return got
}

func isRuneBoundary(text string, offset int) bool {
	return offset == len(text) || utf8.RuneStart(text[offset])
}

func TestPlainTextExcerpt_HardCutSnapsOutwardToRuneBoundaries(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		args      excerptArgs
		wantStart int
		wantEnd   int
	}{
		// ü occupies [0,2), b [2,3).
		{name: "start inside two-byte rune", text: "über", args: noKeep(1, 3), wantStart: 0, wantEnd: 3},
		// € runes occupy [0,3) [3,6) [6,9).
		{name: "both offsets inside three-byte runes", text: "€€€", args: noKeep(1, 5), wantStart: 0, wantEnd: 6},
		{name: "end inside last three-byte rune", text: "€€€", args: noKeep(3, 7), wantStart: 3, wantEnd: 9},
		// 😀 runes occupy [0,4) [4,8).
		{name: "both offsets inside four-byte emoji", text: "😀😀", args: noKeep(2, 6), wantStart: 0, wantEnd: 8},
		{name: "window inside a single four-byte emoji", text: "😀😀", args: noKeep(5, 7), wantStart: 4, wantEnd: 8},
		// CJK runes are 3 bytes each with no spaces, so alignment finds no word and hard-cuts.
		{name: "cjk run without spaces", text: "日本語テキスト", args: noKeep(4, 8), wantStart: 3, wantEnd: 9},
		{name: "cjk run empty window inside a rune", text: "日本語テキスト", args: noKeep(4, 4), wantStart: 3, wantEnd: 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := requireRuneSafeExcerpt(t, tc.text, tc.args)
			assert.Equal(t, tc.wantStart, got.Start, "Start")
			assert.Equal(t, tc.wantEnd, got.End, "End")
		})
	}
}

func TestPlainTextExcerpt_KeepInsideRuneIncludesEnclosingRune(t *testing.T) {
	cases := []struct {
		name               string
		text               string
		args               excerptArgs
		runeStart, runeEnd int
	}{
		// "a ü b": ü occupies [2,4).
		{name: "keep starts inside two-byte rune", text: "a ü b", args: excerptArgs{start: 3, end: 4, keepStart: 3, keepEnd: 4}, runeStart: 2, runeEnd: 4},
		// "x €y z": € occupies [2,5).
		{name: "keep inside three-byte rune", text: "x €y z", args: excerptArgs{start: 0, end: 6, keepStart: 3, keepEnd: 4}, runeStart: 2, runeEnd: 5},
		// "go 😀 now": 😀 occupies [3,7).
		{name: "keep ends inside four-byte emoji", text: "go 😀 now", args: excerptArgs{start: 3, end: 5, keepStart: 3, keepEnd: 5}, runeStart: 3, runeEnd: 7},
		// "日本語テキスト": 本 occupies [3,6), 語 [6,9).
		{name: "keep straddles cjk runes", text: "日本語テキスト", args: excerptArgs{start: 4, end: 7, keepStart: 4, keepEnd: 7}, runeStart: 3, runeEnd: 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := requireRuneSafeExcerpt(t, tc.text, tc.args)
			assert.LessOrEqual(t, got.Start, tc.runeStart, "Start")
			assert.GreaterOrEqual(t, got.End, tc.runeEnd, "End")
		})
	}
}

func TestPlainTextExcerpt_AllOffsetPairsStayOnRuneBoundaries(t *testing.T) {
	texts := []string{
		"ü€😀 日本 a·b",
		"word 😀😀 — mot ünï",
		"日本語テキスト",
	}
	for _, text := range texts {
		t.Run(text, func(t *testing.T) {
			for start := 0; start <= len(text); start++ {
				for end := 0; end <= len(text); end++ {
					requireRuneSafeExcerpt(t, text, noKeep(start, end))
					requireRuneSafeExcerpt(t, text, excerptArgs{start: start, end: end, keepStart: start, keepEnd: end})
				}
			}
		})
	}
}

func TestPlainTextExcerpt_CutFlagsIgnoreEdgeWhitespace(t *testing.T) {
	cases := []struct {
		name          string
		text          string
		args          excerptArgs
		wantCutBefore bool
		wantCutAfter  bool
	}{
		{name: "only whitespace before start", text: "   alpha beta", args: noKeep(3, 13), wantCutBefore: false, wantCutAfter: false},
		{name: "only whitespace after end", text: "alpha beta   ", args: noKeep(0, 10), wantCutBefore: false, wantCutAfter: false},
		{name: "only whitespace on both sides", text: "\n\t alpha beta \n", args: noKeep(3, 13), wantCutBefore: false, wantCutAfter: false},
		{name: "text before start", text: "gamma alpha beta", args: noKeep(6, 16), wantCutBefore: true, wantCutAfter: false},
		{name: "text after end", text: "alpha beta gamma", args: noKeep(0, 10), wantCutBefore: false, wantCutAfter: true},
		{name: "text on both sides behind whitespace", text: "x   alpha   y", args: noKeep(4, 9), wantCutBefore: true, wantCutAfter: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := requireRuneSafeExcerpt(t, tc.text, tc.args)
			assert.Equal(t, tc.wantCutBefore, got.CutBefore, "CutBefore")
			assert.Equal(t, tc.wantCutAfter, got.CutAfter, "CutAfter")
			assert.Equal(t, strings.TrimSpace(tc.text[:got.Start]) != "", got.CutBefore, "CutBefore vs leading text")
			assert.Equal(t, strings.TrimSpace(tc.text[got.End:]) != "", got.CutAfter, "CutAfter vs trailing text")
		})
	}
}
