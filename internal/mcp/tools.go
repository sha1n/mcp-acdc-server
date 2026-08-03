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
	URI string `json:"uri" jsonschema_description:"The resource URI, exactly as returned by search or a resource listing (e.g. acdc://guides/setup#install)"`
}

// RegisterSearchTool registers the search tool with the server
func RegisterSearchTool(s *mcp.Server, searchService search.Searcher, revalidator Revalidator, settings config.SearchSettings, metadata domain.ToolMetadata) {
	mcp.AddTool(s,
		&mcp.Tool{
			Name:        metadata.Name,
			Description: metadata.Description,
			// InputSchema auto-generated from SearchToolArgument
		},
		NewSearchToolHandler(searchService, revalidator, settings),
	)
}

// RegisterReadTool registers the read tool with the server
func RegisterReadTool(s *mcp.Server, catalog resources.Catalog, revalidator Revalidator, metadata domain.ToolMetadata) {
	mcp.AddTool(s,
		&mcp.Tool{
			Name:        metadata.Name,
			Description: metadata.Description,
			// InputSchema auto-generated from ReadToolArgument
		},
		NewReadToolHandler(catalog, revalidator),
	)
}

// NewSearchToolHandler creates the handler for the search tool
func NewSearchToolHandler(searchService search.Searcher, revalidator Revalidator, settings config.SearchSettings) mcp.ToolHandlerFor[SearchToolArgument, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, args SearchToolArgument) (*mcp.CallToolResult, any, error) {
		revalidator.Revalidate(ctx)
		// Args are already validated and unmarshaled by SDK via jsonschema tags
		slog.Info("Search request", "query", args.Query)

		selected, err := fillSearchPage(searchService, args.Query, settings.ResultMode, settings.MaxResults)
		if err != nil {
			slog.Error("Search failed", "query", args.Query, "error", err)
			return nil, nil, err
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: formatSearchResults(args.Query, selected, settings.ResultMode)},
			},
		}, nil, nil
	}
}

// NewReadToolHandler creates the handler for the read tool
func NewReadToolHandler(catalog resources.Catalog, revalidator Revalidator) mcp.ToolHandlerFor[ReadToolArgument, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, args ReadToolArgument) (*mcp.CallToolResult, any, error) {
		revalidator.Revalidate(ctx)
		// Args are already validated and unmarshaled by SDK via jsonschema tags
		slog.Info("Get resource request", "uri", args.URI)

		content, err := catalog.ReadResource(args.URI)
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
