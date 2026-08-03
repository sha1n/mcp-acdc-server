package mcp

import (
	"fmt"
	"strings"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/search"
)

const (
	// candidateMultiplier sizes the first candidate window against the result limit.
	candidateMultiplier = 5
	// candidateGrowthFactor widens the window on each further retrieval.
	candidateGrowthFactor = 4
	// maxCandidateRetrievals bounds the widening. Query cost grows roughly
	// linearly with the window, so the ceiling is what keeps a flooded query from
	// paying for the whole corpus.
	maxCandidateRetrievals = 3
)

// fillSearchPage retrieves candidates and selects a page from them, widening the
// candidate window while the page is short and the candidate set was truncated.
// The per-source cap discards candidates, so a document occupying the leading
// ranks would otherwise shrink the page rather than merely lose its own
// duplicates — a fixed window returns whatever survives the cap.
//
// Each round selects from a single Search call rather than accumulating across
// rounds: a refresh may swap the index between retrievals, and a page assembled
// from two index generations could carry a stale chunk beside its replacement.
func fillSearchPage(searcher search.Searcher, query string, mode config.SearchResultMode, limit int) ([]search.SearchResult, error) {
	window := limit * candidateMultiplier

	for retrieval := 1; ; retrieval++ {
		results, err := searcher.Search(query, window)
		if err != nil {
			return nil, err
		}

		selected := selectSearchResults(results, mode, limit)
		// A short result set means the candidates are exhausted, so a wider
		// window cannot add anything. Comparisons are >=, not ==, so a
		// non-positive limit terminates on the first retrieval.
		if len(selected) >= limit || len(results) < window || retrieval >= maxCandidateRetrievals {
			return selected, nil
		}

		window *= candidateGrowthFactor
	}
}

func selectSearchResults(results []search.SearchResult, mode config.SearchResultMode, limit int) []search.SearchResult {
	if limit <= 0 {
		return nil
	}

	selected := make([]search.SearchResult, 0, min(len(results), limit))
	perSource := make(map[string]int)

	for _, result := range results {
		if len(selected) == limit {
			break
		}

		if mode == config.SearchResultModeReferences {
			if perSource[result.SourceID] > 0 {
				continue
			}
		} else if perSource[result.SourceID] == 2 {
			continue
		}

		selected = append(selected, result)
		perSource[result.SourceID]++
	}

	return selected
}

func formatSearchResults(query string, results []search.SearchResult, mode config.SearchResultMode) string {
	if len(results) == 0 {
		return fmt.Sprintf("No results found for '%s'", query)
	}

	var output strings.Builder
	fmt.Fprintf(&output, "Search results for '%s':\n\n", query)
	for _, result := range results {
		fmt.Fprintf(&output, "- **%s** · [%s](%s)\n", result.SourceTitle, result.ChunkURI, result.ChunkURI)
		fmt.Fprintf(&output, "  - %s · lines %d-%d · score %.2f\n", searchBreadcrumb(result), result.StartLine, result.EndLine, result.Score)
		fmt.Fprintf(&output, "  - %s\n", result.Snippet)

		if mode == config.SearchResultModeContent {
			fmt.Fprintf(&output, "\n%s\n", result.Content)
		}

		output.WriteString("\n")
	}

	return output.String()
}

func searchBreadcrumb(result search.SearchResult) string {
	parts := make([]string, 0, len(result.HeadingPath)+1)
	if result.SourcePath != "" {
		parts = append(parts, result.SourcePath)
	}
	parts = append(parts, result.HeadingPath...)
	return strings.Join(parts, " > ")
}
