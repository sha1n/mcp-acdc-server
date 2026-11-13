package search

import (
	"testing"

	"github.com/blevesearch/bleve/v2/search"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/stretchr/testify/require"
)

func TestProjectedHits_NestedHitsSharingStartKeepLongest(t *testing.T) {
	plain := content.ProjectPlainText("foobar baz")
	terms := search.TermLocationMap{
		"foo":    search.Locations{{Start: 0, End: 3}},
		"foobar": search.Locations{{Start: 0, End: 6}},
		"baz":    search.Locations{{Start: 7, End: 10}},
	}

	require.Equal(t, []textRange{{start: 0, end: 6}, {start: 7, end: 10}}, projectedHits(plain, terms))
}

func TestProjectedHits_NestedHitsSharingStartInOneTermKeepLongest(t *testing.T) {
	plain := content.ProjectPlainText("foobar baz")
	tests := []struct {
		name      string
		locations search.Locations
	}{
		{name: "short first", locations: search.Locations{{Start: 0, End: 3}, {Start: 0, End: 6}, {Start: 7, End: 10}}},
		{name: "long first", locations: search.Locations{{Start: 0, End: 6}, {Start: 0, End: 3}, {Start: 7, End: 10}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			terms := search.TermLocationMap{"foo": tt.locations}

			require.Equal(t, []textRange{{start: 0, end: 6}, {start: 7, end: 10}}, projectedHits(plain, terms))
		})
	}
}
