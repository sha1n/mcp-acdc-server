package mcp

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/search"
)

// SearchToolArgument represents arguments for search tool
type SearchToolArgument struct {
	Query string `json:"query" jsonschema_description:"The search query. Use natural language or keywords."`
}

// ReadToolArgument represents arguments for read tool
type ReadToolArgument struct {
	URI string `json:"uri" jsonschema_description:"The acdc:// URI of the resource to fetch"`
}

// RegisterSearchTool registers the search tool with the server
func RegisterSearchTool(s *mcp.Server, searchService search.Searcher, settings config.SearchSettings, metadata domain.ToolMetadata) {
	mcp.AddTool(s,
		&mcp.Tool{
			Name:        metadata.Name,
			Description: metadata.Description,
			// InputSchema auto-generated from SearchToolArgument
		},
		NewSearchToolHandler(searchService, settings),
	)
}

// RegisterReadTool registers the read tool with the server
func RegisterReadTool(s *mcp.Server, resourceProvider *resources.ResourceProvider, metadata domain.ToolMetadata) {
	mcp.AddTool(s,
		&mcp.Tool{
			Name:        metadata.Name,
			Description: metadata.Description,
			// InputSchema auto-generated from ReadToolArgument
		},
		NewReadToolHandler(resourceProvider),
	)
}

// NewSearchToolHandler creates the handler for the search tool
func NewSearchToolHandler(searchService search.Searcher, settings config.SearchSettings) mcp.ToolHandlerFor[SearchToolArgument, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, args SearchToolArgument) (*mcp.CallToolResult, any, error) {
		// Args are already validated and unmarshaled by SDK via jsonschema tags
		slog.Info("Search request", "query", args.Query)

		results, err := searchService.Search(args.Query, settings.MaxResults*candidateMultiplier)
		if err != nil {
			slog.Error("Search failed", "query", args.Query, "error", err)
			return nil, nil, err
		}

		selected := selectSearchResults(results, settings.ResultMode, settings.MaxResults)

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: formatSearchResults(args.Query, selected, settings.ResultMode)},
			},
		}, nil, nil
	}
}

// NewReadToolHandler creates the handler for the read tool
func NewReadToolHandler(resourceProvider *resources.ResourceProvider) mcp.ToolHandlerFor[ReadToolArgument, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, args ReadToolArgument) (*mcp.CallToolResult, any, error) {
		// Args are already validated and unmarshaled by SDK via jsonschema tags
		slog.Info("Get resource request", "uri", args.URI)

		content, err := resourceProvider.ReadResource(args.URI)
		if err != nil {
			slog.Error("Get resource failed", "uri", args.URI, "error", err)
			return nil, nil, err
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: content},
			},
		}, nil, nil
	}
}
