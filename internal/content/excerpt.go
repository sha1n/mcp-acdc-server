package content

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Excerpt is a range of a PlainText's Text, in bytes, cut for display.
// CutBefore and CutAfter report whether visible text was left out on each side.
type Excerpt struct {
	Start, End          int
	CutBefore, CutAfter bool
}

type span struct{ start, end int }

func (s span) empty() bool { return s.start >= s.end }

// Excerpt narrows Text[start:end] to whole words and drops whitespace and the
// block, row or cell separators the projection inserted at either edge;
// authored text is never dropped. A non-empty Text[keepStart:keepEnd] is
// always included whole, so a caller can protect the matches it chose the
// range for. A range holding no whole word keeps its hard edges rather than
// vanishing. Offsets are clamped into the text and always land on rune
// boundaries.
func (p PlainText) Excerpt(start, end, keepStart, keepEnd int) Excerpt {
	text := p.Text
	window := span{clampOffset(start, len(text)), clampOffset(end, len(text))}
	window.end = max(window.start, window.end)
	keep := span{clampOffset(keepStart, len(text)), clampOffset(keepEnd, len(text))}
	if !keep.empty() {
		keep = snapOutward(text, keep)
		window = span{min(window.start, keep.start), max(window.end, keep.end)}
	}

	window = p.trimEdges(alignWords(text, window, keep), keep)

	return Excerpt{
		Start:     window.start,
		End:       window.end,
		CutBefore: strings.TrimSpace(text[:window.start]) != "",
		CutAfter:  strings.TrimSpace(text[window.end:]) != "",
	}
}

func clampOffset(offset, length int) int {
	return min(max(offset, 0), length)
}

func startLimit(window, keep span) int {
	if keep.empty() {
		return window.end
	}
	return keep.start
}

func endLimit(window, keep span) int {
	if keep.empty() {
		return window.start
	}
	return keep.end
}

func alignWords(text string, window, keep span) span {
	aligned := span{start: alignWordStart(text, window.start, startLimit(window, keep))}
	aligned.end = alignWordEnd(text, window.end, endLimit(span{aligned.start, window.end}, keep))
	if aligned.empty() {
		return snapOutward(text, window)
	}
	return aligned
}

func alignWordStart(text string, from, limit int) int {
	for i := from; i < limit; i++ {
		if isWordStart(text, i) {
			return i
		}
	}
	return limit
}

func alignWordEnd(text string, from, limit int) int {
	for i := from; i > limit; i-- {
		if isWordEnd(text, i) {
			return i
		}
	}
	return limit
}

func isWordStart(text string, i int) bool {
	return onRuneBoundary(text, i) && blankBefore(text, i) && !blankAfter(text, i)
}

func isWordEnd(text string, i int) bool {
	return onRuneBoundary(text, i) && !blankBefore(text, i) && blankAfter(text, i)
}

func blankBefore(text string, i int) bool {
	previous, _ := utf8.DecodeLastRuneInString(text[:i])
	return i == 0 || unicode.IsSpace(previous)
}

func blankAfter(text string, i int) bool {
	next, _ := utf8.DecodeRuneInString(text[i:])
	return i == len(text) || unicode.IsSpace(next)
}

func onRuneBoundary(text string, i int) bool {
	return i == len(text) || utf8.RuneStart(text[i])
}

func snapOutward(text string, s span) span {
	for s.start > 0 && !onRuneBoundary(text, s.start) {
		s.start--
	}
	for !onRuneBoundary(text, s.end) {
		s.end++
	}
	return s
}

func (p PlainText) trimEdges(window, keep span) span {
	window.start = p.trimLeading(window.start, startLimit(window, keep))
	window.end = p.trimTrailing(window.end, endLimit(window, keep))
	return window
}

func (p PlainText) trimLeading(start, limit int) int {
	for {
		start = limit - len(strings.TrimLeftFunc(p.Text[start:limit], unicode.IsSpace))
		separator, ok := p.separatorAt(start)
		if !ok || start == limit {
			return start
		}
		start = min(separator.end, limit)
	}
}

func (p PlainText) trimTrailing(end, limit int) int {
	for {
		end = limit + len(strings.TrimRightFunc(p.Text[limit:end], unicode.IsSpace))
		separator, ok := p.separatorAt(end - 1)
		if !ok || end == limit {
			return end
		}
		end = max(separator.start, limit)
	}
}

// separatorAt finds the separator the projection inserted over byte i, if any.
func (p PlainText) separatorAt(i int) (span, bool) {
	n := sort.Search(len(p.separators), func(k int) bool { return p.separators[k].end > i })
	if n == len(p.separators) || p.separators[n].start > i {
		return span{}, false
	}
	return p.separators[n], true
}
