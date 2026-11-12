package content

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yuin/goldmark/ast"
)

func TestHeadingIDsReserving_WithoutReservationsEqualsHeadingIDs(t *testing.T) {
	cases := []struct {
		name     string
		markdown string
	}{
		{name: "distinct headings", markdown: "# Alpha\n\n## Beta\n"},
		{name: "duplicate headings", markdown: "# Alpha\n\n## Alpha\n\n### Alpha\n"},
		{name: "preamble collision", markdown: "Intro.\n\n# Document\n"},
		{name: "nested heading", markdown: "# Alpha\n\n> # Beta\n"},
		{name: "empty headings", markdown: "#\n\n# Alpha\n\n##\n"},
		{name: "no headings", markdown: "Just text.\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := []byte(tc.markdown)
			doc := parseGFM(source)

			for _, reserved := range [][]string{nil, {}} {
				require.Equal(t, HeadingIDs(doc, source), HeadingIDsReserving(doc, source, reserved, nil))
			}
		})
	}
}

func TestHeadingIDsReserving_NeverReturnsReservedIDs(t *testing.T) {
	cases := []struct {
		name     string
		markdown string
		reserved []string
		want     []string
	}{
		{
			name:     "reserved slug forces the first suffix",
			markdown: "# A\n",
			reserved: []string{"a"},
			want:     []string{"a-1"},
		},
		{
			name:     "reserved suffixed ids are skipped",
			markdown: "# A\n",
			reserved: []string{"a", "a-1"},
			want:     []string{"a-2"},
		},
		{
			name:     "reserved suffix alone leaves the slug available",
			markdown: "# A\n\n# A\n",
			reserved: []string{"a-1"},
			want:     []string{"a", "a-2"},
		},
		{
			name:     "duplicate headings stay unique around reservations",
			markdown: "# A\n\n# A\n\n# A\n",
			reserved: []string{"a", "a-2"},
			want:     []string{"a-1", "a-3", "a-4"},
		},
		{
			name:     "unrelated reservations do not shift ids",
			markdown: "# A\n\n# B\n",
			reserved: []string{"c", "c-1"},
			want:     []string{"a", "b"},
		},
		{
			name:     "reserved fallback slug",
			markdown: "# !!!\n\n# ???\n",
			reserved: []string{"section"},
			want:     []string{"section-1", "section-2"},
		},
		{
			name:     "duplicate reservations are harmless",
			markdown: "# A\n",
			reserved: []string{"a", "a"},
			want:     []string{"a-1"},
		},
		{
			name:     "empty headings get no id",
			markdown: "#\n\n# A\n\n##\n",
			reserved: []string{"a"},
			want:     []string{"", "a-1", ""},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := []byte(tc.markdown)
			doc := parseGFM(source)

			ids := HeadingIDsReserving(doc, source, tc.reserved, nil)

			require.Equal(t, tc.want, idsInDocumentOrder(doc, ids))
			for _, id := range ids {
				require.NotContains(t, tc.reserved, id)
			}
		})
	}
}

func TestHeadingIDsReserving_SkipsAssignedHeadings(t *testing.T) {
	source := []byte("# A\n\n# A\n\n# B\n")
	doc := parseGFM(source)
	headings := collectHeadings(doc)
	assigned := map[*ast.Heading]bool{headings[0]: true}

	ids := HeadingIDsReserving(doc, source, []string{"a"}, assigned)

	require.Equal(t, []string{"", "a-1", "b"}, idsInDocumentOrder(doc, ids))
	require.Len(t, ids, 2)
}

func idsInDocumentOrder(doc ast.Node, ids map[*ast.Heading]string) []string {
	ordered := []string{}
	for _, heading := range collectHeadings(doc) {
		ordered = append(ordered, ids[heading])
	}
	return ordered
}
