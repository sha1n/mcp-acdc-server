package mcp

import (
	"fmt"
	"strings"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/search"
)

const candidateMultiplier = 5

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
