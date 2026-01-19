package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sha1n/mcp-acdc-server-go/internal/domain"
	"github.com/sha1n/mcp-acdc-server-go/internal/search"
)

// SearchToolArgument represents arguments for search tool
type SearchToolArgument struct {
	Query string `json:"query"`
}

// RegisterSearchTool registers the search tool with the server
func RegisterSearchTool(s *server.MCPServer, searchService search.Searcher, metadata domain.ToolMetadata) {
	tool := mcp.NewTool(
		metadata.Name,
		mcp.WithDescription(metadata.Description),
		mcp.WithString("query", mcp.Description("The search query. Use natural language or keywords.")),
	)

	s.AddTool(tool, NewSearchToolHandler(searchService))
}

// NewSearchToolHandler creates the handler for the search tool
func NewSearchToolHandler(searchService search.Searcher) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, ok := req.Params.Arguments.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid arguments format")
		}

		query, ok := args["query"].(string)
		if !ok {
			return nil, fmt.Errorf("missing 'query' argument")
		}

		results, err := searchService.Search(query, nil)
		if err != nil {
			return nil, err
		}

		var sb strings.Builder
		if len(results) == 0 {
			sb.WriteString(fmt.Sprintf("No results found for '%s'", query))
		} else {
			sb.WriteString(fmt.Sprintf("Search results for '%s':\n\n", query))
			for _, r := range results {
				sb.WriteString(fmt.Sprintf("- [%s](%s): %s\n\n", r.Name, r.URI, r.Snippet))
			}
		}

		return mcp.NewToolResultText(sb.String()), nil
	}
}
