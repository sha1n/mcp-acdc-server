package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/search"
)

// SearchToolArgument represents arguments for search tool
type SearchToolArgument struct {
	Query  string `json:"query" jsonschema_description:"The search query. Use natural language or keywords."`
	Source string `json:"source,omitempty" jsonschema_description:"Optional: filter results to a specific content source."`
}

// ReadToolArgument represents arguments for read tool
type ReadToolArgument struct {
	URI string `json:"uri" jsonschema_description:"The acdc:// URI of the resource to fetch"`
}

// RegisterSearchTool registers the search tool with the server
func RegisterSearchTool(s *mcp.Server, searchService search.Searcher, metadata domain.ToolMetadata) {
	mcp.AddTool(s,
		&mcp.Tool{
			Name:        metadata.Name,
			Description: metadata.Description,
			// InputSchema auto-generated from SearchToolArgument
		},
		NewSearchToolHandler(searchService),
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
func NewSearchToolHandler(searchService search.Searcher) mcp.ToolHandlerFor[SearchToolArgument, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, args SearchToolArgument) (*mcp.CallToolResult, any, error) {
		// Args are already validated and unmarshaled by SDK via jsonschema tags
		slog.Info("Search request", "query", args.Query, "source", args.Source)

		// Build search options with optional source filter
		var opts *search.SearchOptions
		if args.Source != "" {
			opts = &search.SearchOptions{Source: args.Source}
		}

		results, err := searchService.Search(args.Query, opts)
		if err != nil {
			slog.Error("Search failed", "query", args.Query, "source", args.Source, "error", err)
			return nil, nil, err
		}

		var sb strings.Builder
		if len(results) == 0 {
			if args.Source != "" {
				sb.WriteString(fmt.Sprintf("No results found for '%s' in source '%s'", args.Query, args.Source))
			} else {
				sb.WriteString(fmt.Sprintf("No results found for '%s'", args.Query))
			}
		} else {
			if args.Source != "" {
				sb.WriteString(fmt.Sprintf("Search results for '%s' in source '%s':\n\n", args.Query, args.Source))
			} else {
				sb.WriteString(fmt.Sprintf("Search results for '%s':\n\n", args.Query))
			}
			for _, r := range results {
				if r.Source != "" {
					sb.WriteString(fmt.Sprintf("- [%s] [%s](%s): %s\n\n", r.Source, r.Name, r.URI, r.Snippet))
				} else {
					sb.WriteString(fmt.Sprintf("- [%s](%s): %s\n\n", r.Name, r.URI, r.Snippet))
				}
			}
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: sb.String()},
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
