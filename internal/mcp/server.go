package mcp

import (
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/prompts"
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
	promptProvider *prompts.PromptProvider,
	searchService search.Searcher,
) *mcp.Server {
	// Create server with official SDK
	s := mcp.NewServer(&mcp.Implementation{
		Name:    metadata.Server.Name,
		Version: metadata.Server.Version,
	}, nil)
	// Note: Instructions are stored in metadata but not directly supported by official SDK

	// Register Resources
	for _, res := range resourceProvider.ListResources() {
		// Capture uri for closure
		uri := res.URI

		s.AddResource(&mcp.Resource{
			URI:         uri,
			Name:        res.Name,
			Description: res.Description,
			MIMEType:    res.MIMEType,
		}, makeResourceHandler(resourceProvider, uri))
	}

	// Register Prompts
	for _, p := range promptProvider.ListPrompts() {
		// Capture name for closure
		name := p.Name

		s.AddPrompt(&mcp.Prompt{
			Name:        name,
			Description: p.Description,
			Arguments:   p.Arguments,
		}, makePromptHandler(promptProvider, name))

		slog.Info("Registered prompt", "name", name)
	}

	// Register Tools
	RegisterSearchTool(s, searchService, metadata.GetToolMetadata(ToolNameSearch))
	slog.Info("Registered tool", "name", ToolNameSearch)

	RegisterReadTool(s, resourceProvider, metadata.GetToolMetadata(ToolNameRead))
	slog.Info("Registered tool", "name", ToolNameRead)

	return s
}
