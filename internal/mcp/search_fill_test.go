package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/stretchr/testify/require"
)

// These cases drive the per-source cap through a real index and the real
// ranking model. Every other presenter test feeds hand-built SearchResult
// values, so none of them can see the cap and the scorer disagreeing about how
// many distinct sources the candidate window holds.

// floodedCorpus builds a corpus in which one document owns the leading ranks:
// floodChunks strongly matching sections in a single source, plus otherSources
// documents that match the same query once and more weakly.
func floodedCorpus(floodChunks, otherSources int) []domain.Chunk {
	chunks := make([]domain.Chunk, 0, floodChunks+otherSources)

	for i := 0; i < floodChunks; i++ {
		chunks = append(chunks, domain.Chunk{
			ID:          fmt.Sprintf("flood-%d", i),
			SourceID:    "flood",
			SourceURI:   "acdc://flood",
			ChunkURI:    fmt.Sprintf("acdc://flood#section-%d", i),
			SourceTitle: "Widget Handbook",
			SourcePath:  "docs/widget-handbook.md",
			PathLabels:  []string{"docs", "widget", "handbook"},
			HeadingPath: []string{"Widget Handbook", fmt.Sprintf("Widget detail %d", i)},
			Content:     "Widget widget widget. Everything about the widget in detail.",
		})
	}

	for i := 0; i < otherSources; i++ {
		chunks = append(chunks, domain.Chunk{
			ID:          fmt.Sprintf("other-%d-a", i),
			SourceID:    fmt.Sprintf("other-%d", i),
			SourceURI:   fmt.Sprintf("acdc://other-%d", i),
			ChunkURI:    fmt.Sprintf("acdc://other-%d#one", i),
			SourceTitle: fmt.Sprintf("Note %d", i),
			SourcePath:  fmt.Sprintf("docs/notes/note-%d.md", i),
			PathLabels:  []string{"docs", "notes", fmt.Sprintf("note-%d", i)},
			HeadingPath: []string{fmt.Sprintf("Note %d", i)},
			Content:     "A passing mention of a widget among unrelated prose.",
		})
		chunks = append(chunks, domain.Chunk{
			ID:          fmt.Sprintf("other-%d-b", i),
			SourceID:    fmt.Sprintf("other-%d", i),
			SourceURI:   fmt.Sprintf("acdc://other-%d", i),
			ChunkURI:    fmt.Sprintf("acdc://other-%d#two", i),
			SourceTitle: fmt.Sprintf("Note %d", i),
			SourcePath:  fmt.Sprintf("docs/notes/note-%d.md", i),
			PathLabels:  []string{"docs", "notes", fmt.Sprintf("note-%d", i)},
			HeadingPath: []string{fmt.Sprintf("Note %d", i), "Aside"},
			Content:     "Another passing mention of a widget among unrelated prose.",
		})
	}

	return chunks
}

func indexedService(t *testing.T, chunks []domain.Chunk) *search.Service {
	t.Helper()

	service := search.NewService(config.SearchSettings{
		MaxResults:    10,
		InMemory:      true,
		KeywordsBoost: config.DefaultKeywordsBoost,
		HeadingBoost:  config.DefaultHeadingBoost,
		TitleBoost:    config.DefaultTitleBoost,
		PathBoost:     config.DefaultPathBoost,
		ContentBoost:  config.DefaultContentBoost,
	})
	t.Cleanup(service.Close)

	stream := make(chan domain.Chunk, len(chunks))
	for _, chunk := range chunks {
		stream <- chunk
	}
	close(stream)
	require.NoError(t, service.Index(context.Background(), stream))

	return service
}

// searchPageURIs runs the search tool and returns the chunk URIs it rendered.
func searchPageURIs(t *testing.T, service search.Searcher, mode config.SearchResultMode, query string) []string {
	t.Helper()

	handler := NewSearchToolHandler(service, noopRevalidator{}, config.SearchSettings{MaxResults: 10, ResultMode: mode})
	result, _, err := handler(context.Background(), nil, SearchToolArgument{Query: query})
	require.NoError(t, err)

	text := result.Content[0].(*mcp.TextContent).Text
	var uris []string
	for _, line := range strings.Split(text, "\n") {
		// Only the header line of a result carries its URI. A snippet can
		// contain a markdown link of its own, so matching "](" anywhere would
		// count chunk content as results.
		if !strings.HasPrefix(line, "- **") {
			continue
		}
		if open := strings.LastIndex(line, "]("); open >= 0 {
			uris = append(uris, strings.TrimSuffix(line[open+2:], ")"))
		}
	}
	return uris
}

// chunksPerSource counts the rendered chunks each source document contributed.
func chunksPerSource(uris []string) map[string]int {
	counts := make(map[string]int, len(uris))
	for _, uri := range uris {
		source, _, _ := strings.Cut(uri, "#")
		counts[source]++
	}
	return counts
}

func maxChunksPerSource(uris []string) int {
	most := 0
	for _, count := range chunksPerSource(uris) {
		most = max(most, count)
	}
	return most
}

// A document that floods the leading ranks must not shrink the page. With 60
// strongly matching chunks in one source, the first candidate window holds that
// source alone, and the per-source cap discards all but one of them.
func TestSearchToolHandler_FloodedRanksStillFillThePage(t *testing.T) {
	service := indexedService(t, floodedCorpus(60, 12))

	t.Run("references keeps one chunk per source and still fills the page", func(t *testing.T) {
		uris := searchPageURIs(t, service, config.SearchResultModeReferences, "widget")

		require.Len(t, uris, 10)
		require.Equal(t, 1, maxChunksPerSource(uris))
	})

	t.Run("content keeps two chunks per source and still fills the page", func(t *testing.T) {
		uris := searchPageURIs(t, service, config.SearchResultModeContent, "widget")

		require.Len(t, uris, 10)
		require.Equal(t, 2, maxChunksPerSource(uris))
		require.Equal(t, 2, chunksPerSource(uris)["acdc://flood"])
	})
}

// The widening is bounded on both sides: a corpus that cannot fill the page
// costs one retrieval, and a corpus that can never fill it stops at the ceiling
// instead of walking the whole candidate set.
func TestSearchToolHandler_CandidateWindowWidensOnlyAsFarAsNeeded(t *testing.T) {
	tests := []struct {
		name           string
		chunks         []domain.Chunk
		wantResults    int
		wantRetrievals []int
		wantRationale  string
	}{
		{
			name:           "candidates exhausted, so one retrieval",
			chunks:         floodedCorpus(3, 2),
			wantResults:    3,
			wantRetrievals: []int{50},
			wantRationale:  "a short candidate set cannot grow, so widening the window buys nothing",
		},
		{
			name:           "page fills once the flood is out of the way",
			chunks:         floodedCorpus(60, 12),
			wantResults:    10,
			wantRetrievals: []int{50, 200},
			wantRationale:  "60 flooding chunks own the first window; the second reaches the other documents",
		},
		{
			name:           "a full page stops the widening even with candidates left over",
			chunks:         floodedCorpus(60, 200),
			wantResults:    10,
			wantRetrievals: []int{50, 200},
			wantRationale:  "the second window is truncated, so only the full page can stop the widening here",
		},
		{
			name:           "a flood deeper than the ceiling still shortens the page",
			chunks:         floodedCorpus(900, 3),
			wantResults:    1,
			wantRetrievals: []int{50, 200, 800},
			wantRationale:  "candidates never run out, so only the retrieval budget stops the widening — the page is short by design rather than by accident",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			counting := &countingSearcher{Searcher: indexedService(t, test.chunks)}

			uris := searchPageURIs(t, counting, config.SearchResultModeReferences, "widget")

			require.Len(t, uris, test.wantResults, test.wantRationale)
			require.Equal(t, test.wantRetrievals, counting.candidateLimits, test.wantRationale)
		})
	}
}

// Nothing validates search.max_results above 0, so a non-positive limit must
// terminate the widening rather than loop on a page that can never fill.
func TestSearchToolHandler_NonPositiveLimitRetrievesOnce(t *testing.T) {
	for _, limit := range []int{0, -1} {
		t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
			counting := &countingSearcher{Searcher: indexedService(t, floodedCorpus(60, 12))}
			handler := NewSearchToolHandler(counting, noopRevalidator{}, config.SearchSettings{
				MaxResults: limit,
				ResultMode: config.SearchResultModeReferences,
			})

			result, _, err := handler(context.Background(), nil, SearchToolArgument{Query: "widget"})

			require.NoError(t, err)
			require.Contains(t, result.Content[0].(*mcp.TextContent).Text, "No results found")
			require.Len(t, counting.candidateLimits, 1)
		})
	}
}

// countingSearcher records every candidate window it is asked for.
type countingSearcher struct {
	search.Searcher
	candidateLimits []int
}

func (s *countingSearcher) Search(query string, candidateLimit int) ([]search.SearchResult, error) {
	s.candidateLimits = append(s.candidateLimits, candidateLimit)
	return s.Searcher.Search(query, candidateLimit)
}
