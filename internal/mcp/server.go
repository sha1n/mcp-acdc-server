package mcp

import (
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/search"
)

const (
	// ToolNameSearch is the name of the search tool
	ToolNameSearch = "search"
	// ToolNameRead is the name of the read tool
	ToolNameRead = "read"
)

// CreateServer creates and configures the MCP server
func CreateServer(
	metadata domain.McpMetadata,
	resourceProvider *resources.ResourceProvider,
	searchService search.Searcher,
) *server.MCPServer {
	// Create server
	s := server.NewMCPServer(
		metadata.Server.Name,
		metadata.Server.Version,
		server.WithInstructions(metadata.Server.Instructions),
	)

	// Register Resources
	for _, res := range resourceProvider.ListResources() {
		// Capture uri for closure
		uri := res.URI

		s.AddResource(mcp.Resource{
			URI:         uri,
			Name:        res.Name,
			Description: res.Description,
			MIMEType:    res.MIMEType,
		}, makeResourceHandler(resourceProvider, uri))
	}

	// Register Tools
	RegisterSearchTool(s, searchService, metadata.GetToolMetadata(ToolNameSearch))
	slog.Info("Registered tool", "name", ToolNameSearch)

	RegisterReadTool(s, resourceProvider, metadata.GetToolMetadata(ToolNameRead))
	slog.Info("Registered tool", "name", ToolNameRead)

	return s
}
