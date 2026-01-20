package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sha1n/mcp-acdc-server-go/internal/domain"
	"github.com/sha1n/mcp-acdc-server-go/internal/resources"
	"github.com/sha1n/mcp-acdc-server-go/internal/search"
)

const (
	// ToolNameSearch is the name of the search tool
	ToolNameSearch = "search"
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
	toolsMap, _ := metadata.ToolsMap()
	if toolMeta, ok := toolsMap[ToolNameSearch]; ok {
		RegisterSearchTool(s, searchService, toolMeta)
	}

	RegisterGetResourceTool(s, resourceProvider)

	return s
}
