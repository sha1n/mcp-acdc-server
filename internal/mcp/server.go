package mcp

import (
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
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

// CreateServer creates and configures the MCP server. The returned
// *ResourceRegistrar has already performed the initial resource
// registration from catalog; a caller that refreshes the catalog later
// drives further registration changes through it.
//
// A nil revalidator is normalized to a no-op, so callers that never refresh
// the catalog (e.g. tests) can omit it.
func CreateServer(
	metadata domain.McpMetadata,
	catalog resources.Catalog,
	promptProvider *prompts.PromptProvider,
	searchService search.Searcher,
	searchSettings config.SearchSettings,
	revalidator Revalidator,
) (*mcp.Server, *ResourceRegistrar) {
	if revalidator == nil {
		revalidator = noopRevalidator{}
	}

	// Create server with official SDK
	s := mcp.NewServer(&mcp.Implementation{
		Name:    metadata.Server.Name,
		Version: metadata.Server.Version,
	}, &mcp.ServerOptions{Instructions: metadata.Server.Instructions})

	registrar := NewResourceRegistrar(s, catalog, revalidator)
	registrar.Sync(catalog)

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
	RegisterSearchTool(s, searchService, revalidator, searchSettings, metadata.GetToolMetadata(ToolNameSearch))
	slog.Info("Registered tool", "name", ToolNameSearch)

	RegisterReadTool(s, catalog, revalidator, metadata.GetToolMetadata(ToolNameRead))
	slog.Info("Registered tool", "name", ToolNameRead)

	return s, registrar
}
