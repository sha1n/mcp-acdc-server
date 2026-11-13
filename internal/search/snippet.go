package search

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/blevesearch/bleve/v2/search"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
)

// SnippetSpan is a run of raw, unescaped snippet text; Hit marks a query term match.
type SnippetSpan struct {
	Text string
	Hit  bool
}

// Hit boundaries are Unicode noncharacters rather than markup so that a
// snippet can be split into spans without escaping. Both runes are stripped
// from the text before delimiting, so content can never forge a hit.
const (
	hitStart = '﷐'
	hitEnd   = '﷑'

	snippetRunes = 200
	ellipsis     = "…"
)

// textRange is a byte range of a projection's text.
type textRange struct {
	start, end int
}

// snippetSpans cuts the plain-text projection of a chunk's markdown around
// its densest run of content-field hits. Hits come from the content field's
// term locations, so they follow its analyzer (stemming, fuzziness) exactly,
// and only those inside text the projection keeps are marked. Without hits it
// shows the start of the projection, or the title when the projection is blank.
func snippetSpans(locations search.FieldTermLocationMap, markdown, title string) []SnippetSpan {
	plain := content.ProjectPlainText(markdown)
	if strings.TrimSpace(plain.Text) == "" {
		return formatSnippet(content.PlainText{Text: title}, nil)
	}
	return formatSnippet(plain, projectedHits(plain, locations[domain.FieldContent]))
}

// projectedHits returns the located hits as ordered, non-overlapping ranges
// of the projection. Several query terms may match the same token.
func projectedHits(plain content.PlainText, terms search.TermLocationMap) []textRange {
	var hits []textRange
	for _, locations := range terms {
		for _, location := range locations {
			if start, end, ok := plain.Locate(int(location.Start), int(location.End)); ok {
				hits = append(hits, textRange{start: start, end: end})
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].start != hits[j].start {
			return hits[i].start < hits[j].start
		}
		return hits[i].end > hits[j].end
	})
	disjoint := hits[:0]
	for _, hit := range hits {
		if len(disjoint) == 0 || hit.start >= disjoint[len(disjoint)-1].end {
			disjoint = append(disjoint, hit)
		}
	}
	return disjoint
}

func formatSnippet(plain content.PlainText, hits []textRange) []SnippetSpan {
	text := plain.Text
	first, count := densestHits(text, hits)
	hits = hits[first : first+count]
	window := snippetWindow(text, hits)
	keep := textRange{}
	if count > 0 {
		keep = textRange{start: hits[0].start, end: hits[count-1].end}
	}
	excerpt := plain.Excerpt(window.start, window.end, keep.start, keep.end)

	var b strings.Builder
	if excerpt.CutBefore {
		b.WriteString(ellipsis)
	}
	curr := excerpt.Start
	for _, hit := range hits {
		b.WriteString(stripHitSentinels(text[curr:hit.start]))
		b.WriteRune(hitStart)
		b.WriteString(stripHitSentinels(text[hit.start:hit.end]))
		b.WriteRune(hitEnd)
		curr = hit.end
	}
	b.WriteString(stripHitSentinels(text[curr:excerpt.End]))
	if excerpt.CutAfter {
		b.WriteString(ellipsis)
	}
	return parseSnippet(b.String())
}

// snippetWindow spans at most snippetRunes runes with the hits centred in it,
// or the start of the text without hits.
func snippetWindow(text string, hits []textRange) textRange {
	if len(hits) == 0 {
		return textRange{end: runesForward(text, 0, snippetRunes)}
	}
	first, last := hits[0], hits[len(hits)-1]
	slack := snippetRunes - utf8.RuneCountInString(text[first.start:last.end])
	end := runesForward(text, runesBack(text, first.start, slack/2), snippetRunes)
	return textRange{start: runesBack(text, end, snippetRunes), end: end}
}

// densestHits returns the first index and length of the longest run of hits
// that fits in one snippet, preferring the earliest.
func densestHits(text string, hits []textRange) (int, int) {
	best, bestCount := 0, 0
	for i := range hits {
		count := 0
		for i+count < len(hits) && utf8.RuneCountInString(text[hits[i].start:hits[i+count].end]) <= snippetRunes {
			count++
		}
		if count > bestCount {
			best, bestCount = i, count
		}
	}
	return best, bestCount
}

func runesForward(text string, offset, runes int) int {
	for ; runes > 0 && offset < len(text); runes-- {
		_, size := utf8.DecodeRuneInString(text[offset:])
		offset += size
	}
	return offset
}

func runesBack(text string, offset, runes int) int {
	for ; runes > 0 && offset > 0; runes-- {
		_, size := utf8.DecodeLastRuneInString(text[:offset])
		offset -= size
	}
	return offset
}

func stripHitSentinels(text string) string {
	return strings.Map(func(r rune) rune {
		if r == hitStart || r == hitEnd {
			return -1
		}
		return r
	}, text)
}

func parseSnippet(formatted string) []SnippetSpan {
	spans := []SnippetSpan{}
	var text strings.Builder
	hit := false
	flush := func() {
		switch {
		case text.Len() == 0:
		case len(spans) > 0 && spans[len(spans)-1].Hit == hit:
			spans[len(spans)-1].Text += text.String()
		default:
			spans = append(spans, SnippetSpan{Text: text.String(), Hit: hit})
		}
		text.Reset()
	}
	for _, r := range formatted {
		if r == hitStart || r == hitEnd {
			flush()
			hit = r == hitStart
			continue
		}
		text.WriteRune(r)
	}
	flush()
	return spans
}
