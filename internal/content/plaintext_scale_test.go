package content

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	scaleTableHeader          = "| Name | Value |\n|---|---|\n"
	scaleTableHeaderProjected = "Name · Value"
	scaleRowSeparator         = " / "
)

func scaleTableRow(i int) string {
	return fmt.Sprintf("| r%05d AT&amp;T \\*x\\* **b** | val%05d |\n", i, i)
}

func scaleTableRowProjected(i int) string {
	return fmt.Sprintf("r%05d AT&T *x* b · val%05d", i, i)
}

func scaleTable(rows int) string {
	var markdown strings.Builder
	markdown.WriteString(scaleTableHeader)
	for i := range rows {
		markdown.WriteString(scaleTableRow(i))
	}
	return markdown.String()
}

// scaleHit is a source range given relative to a table row and the projected
// text it is expected to map to within that row's projection.
type scaleHit struct {
	name          string
	source        string
	sourceOffset  int
	want          string
	wantOffset    int
	wantLocatable bool
}

// scaleRowHits are hand-checked against scaleTableRow(0) and
// scaleTableRowProjected(0); every row has the same fixed-width layout.
var scaleRowHits = []scaleHit{
	{name: "row label", source: "r00000", sourceOffset: 2, want: "r00000", wantOffset: 0, wantLocatable: true},
	{name: "word spanning an entity", source: "AT&amp;T", sourceOffset: 9, want: "AT&T", wantOffset: 7, wantLocatable: true},
	{name: "entity alone", source: "&amp;", sourceOffset: 11, want: "&", wantOffset: 9, wantLocatable: true},
	{name: "verbatim run ending where an entity starts", source: "AT", sourceOffset: 9, want: "AT", wantOffset: 7, wantLocatable: true},
	{name: "verbatim run starting where an entity ends", source: "T", sourceOffset: 16, want: "T", wantOffset: 10, wantLocatable: true},
	{name: "escaped word", source: "\\*x\\*", sourceOffset: 18, want: "*x*", wantOffset: 12, wantLocatable: true},
	{name: "escape alone", source: "\\*", sourceOffset: 18, want: "*", wantOffset: 12, wantLocatable: true},
	{name: "word between escapes", source: "x", sourceOffset: 20, want: "x", wantOffset: 13, wantLocatable: true},
	{name: "emphasised word", source: "b", sourceOffset: 26, want: "b", wantOffset: 16, wantLocatable: true},
	{name: "value cell", source: "val00000", sourceOffset: 32, want: "val00000", wantOffset: 21, wantLocatable: true},
	{name: "inside an entity", source: "amp", sourceOffset: 12},
	{name: "emphasis marker", source: "**", sourceOffset: 24},
	{name: "range spanning a table pipe", source: "| val", sourceOffset: 30},
}

func TestScaleRowHits_MatchTheRowLayout(t *testing.T) {
	row, projected := scaleTableRow(0), scaleTableRowProjected(0)
	for _, hit := range scaleRowHits {
		require.Equal(t, hit.source, row[hit.sourceOffset:hit.sourceOffset+len(hit.source)], hit.name)
		if hit.wantLocatable {
			require.Equal(t, hit.want, projected[hit.wantOffset:hit.wantOffset+len(hit.want)], hit.name)
		}
	}
}

func TestPlainText_LocateMapsHitsAcrossALargeTable(t *testing.T) {
	const rows = 2000
	projection := ProjectPlainText(scaleTable(rows))

	var wantText strings.Builder
	wantText.WriteString(scaleTableHeaderProjected)
	for i := range rows {
		wantText.WriteString(scaleRowSeparator + scaleTableRowProjected(i))
	}
	require.Equal(t, wantText.String(), projection.Text)

	rowSource, rowTarget := len(scaleTableHeader), len(scaleTableHeaderProjected)+len(scaleRowSeparator)
	for i := range rows {
		for _, hit := range scaleRowHits {
			start := rowSource + hit.sourceOffset
			from, to, ok := projection.Locate(start, start+len(hit.source))

			require.Equal(t, hit.wantLocatable, ok, "row %d: %s", i, hit.name)
			if hit.wantLocatable {
				require.Equal(t, rowTarget+hit.wantOffset, from, "row %d: %s", i, hit.name)
				require.Equal(t, rowTarget+hit.wantOffset+len(hit.want), to, "row %d: %s", i, hit.name)
			}
		}
		rowSource += len(scaleTableRow(i))
		rowTarget += len(scaleTableRowProjected(i)) + len(scaleRowSeparator)
	}
}

func BenchmarkPlainTextMapHits_1kRows(b *testing.B) {
	benchmarkPlainTextMapHits(b, 1_000)
}

func BenchmarkPlainTextMapHits_10kRows(b *testing.B) {
	benchmarkPlainTextMapHits(b, 10_000)
}

// benchmarkPlainTextMapHits maps every occurrence of a term common to all
// rows, as a search for that term over the table yields one hit per row.
func benchmarkPlainTextMapHits(b *testing.B, rows int) {
	markdown := scaleTable(rows)
	projection := ProjectPlainText(markdown)
	const term = "AT&amp;T"
	var hits []int
	for offset := 0; ; {
		index := strings.Index(markdown[offset:], term)
		if index < 0 {
			break
		}
		hits = append(hits, offset+index)
		offset += index + len(term)
	}
	require.Len(b, hits, rows)

	for b.Loop() {
		for _, start := range hits {
			if _, _, ok := projection.Locate(start, start+len(term)); !ok {
				b.Fatalf("hit at %d not located", start)
			}
		}
	}
}
