package mcp

import (
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/prompts"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/search"
)

func TestCreateServer(t *testing.T) {
	// Basic smoke test
	serverMeta := domain.ServerMetadata{
		Name:         "test-server",
		Version:      "1.0.0",
		Instructions: "Run tests",
	}
	metadata := domain.McpMetadata{
		Server: serverMeta,
		Tools: []domain.ToolMetadata{
			{Name: "search", Description: "Search tool"},
			{Name: "read", Description: "Read tool"},
		},
	}

	resourceProvider := resources.NewResourceProvider([]resources.ResourceDefinition{})
	promptProvider := prompts.NewPromptProvider([]prompts.PromptDefinition{}, nil)
	searchService := &mockSearcher{}

	server := CreateServer(metadata, resourceProvider, promptProvider, searchService)
	if server == nil {
		t.Fatal("Server should not be nil")
	}
}

type mockSearcher struct{}

func (m *mockSearcher) Search(query string, options *int) ([]search.SearchResult, error) {
	return nil, nil
}

func (m *mockSearcher) Close() {}

func (m *mockSearcher) IndexDocuments(docs []search.Document) error {
	return nil
}
