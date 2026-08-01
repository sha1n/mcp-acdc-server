package mcp

import (
	"context"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/config"
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

	resourceProvider, err := resources.NewResourceProvider([]resources.ResourceDefinition{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	promptProvider := prompts.NewPromptProvider([]prompts.PromptDefinition{}, nil)
	searchService := &mockSearcher{}

	server := CreateServer(metadata, resourceProvider, promptProvider, searchService, config.SearchSettings{MaxResults: 10, ResultMode: config.SearchResultModeReferences})
	if server == nil {
		t.Fatal("Server should not be nil")
	}
}

type mockSearcher struct{}

func (m *mockSearcher) Search(query string, candidateLimit int) ([]search.SearchResult, error) {
	return nil, nil
}

func (m *mockSearcher) Close() {}

func (m *mockSearcher) Index(ctx context.Context, chunks <-chan domain.Chunk) error {
	for range chunks {
		// drain
	}
	return nil
}

func (m *mockSearcher) ReplaceSource(context.Context, string, []domain.Chunk) error { return nil }
func (m *mockSearcher) DeleteSource(context.Context, string) error                  { return nil }
