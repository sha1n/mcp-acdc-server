package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sha1n/mcp-acdc-server-go/internal/domain"
	"github.com/sha1n/mcp-acdc-server-go/internal/resources"
	"github.com/sha1n/mcp-acdc-server-go/internal/search"
)

// SearchToolArgument represents arguments for search tool
type SearchToolArgument struct {
	Query string `json:"query"`
}

// GetResourceToolArgument represents arguments for get_resource tool
type GetResourceToolArgument struct {
	URI string `json:"uri"`
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

// RegisterGetResourceTool registers the get_resource tool with the server
func RegisterGetResourceTool(s *server.MCPServer, resourceProvider *resources.ResourceProvider) {
	tool := mcp.NewTool(
		"get_resource",
		mcp.WithDescription("Get the full content of a resource by its URI"),
		mcp.WithString("uri", mcp.Description("The acdc:// URI of the resource to fetch")),
	)

	s.AddTool(tool, NewGetResourceToolHandler(resourceProvider))
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

		slog.Info("Search request", "query", query)

		results, err := searchService.Search(query, nil)
		if err != nil {
			slog.Error("Search failed", "query", query, "error", err)
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

// NewGetResourceToolHandler creates the handler for the get_resource tool
func NewGetResourceToolHandler(resourceProvider *resources.ResourceProvider) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, ok := req.Params.Arguments.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid arguments format")
		}

		uri, ok := args["uri"].(string)
		if !ok {
			return nil, fmt.Errorf("missing 'uri' argument")
		}

		slog.Info("Get resource request", "uri", uri)

		content, err := resourceProvider.ReadResource(uri)
		if err != nil {
			slog.Error("Get resource failed", "uri", uri, "error", err)
			return nil, err
		}

		return mcp.NewToolResultText(content), nil
	}
}
