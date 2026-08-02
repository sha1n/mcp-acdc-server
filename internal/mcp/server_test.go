package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/prompts"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/stretchr/testify/require"
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

// TestCreateServer_InstructionsReachClient verifies that server.instructions
// from the metadata is actually delivered to a connected client during MCP
// initialization, not just stored on the Go struct.
func TestCreateServer_InstructionsReachClient(t *testing.T) {
	const wantInstructions = "Search the docs before answering questions about this repository."

	metadata := domain.McpMetadata{
		Server: domain.ServerMetadata{
			Name:         "test-server",
			Version:      "1.0.0",
			Instructions: wantInstructions,
		},
		Tools: []domain.ToolMetadata{
			{Name: "search", Description: "Search tool"},
			{Name: "read", Description: "Read tool"},
		},
	}

	resourceProvider, err := resources.NewResourceProvider([]resources.ResourceDefinition{}, nil)
	require.NoError(t, err)
	promptProvider := prompts.NewPromptProvider([]prompts.PromptDefinition{}, nil)

	server := CreateServer(metadata, resourceProvider, promptProvider, &mockSearcher{}, config.SearchSettings{MaxResults: 10, ResultMode: config.SearchResultModeReferences})
	require.NotNil(t, server)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	_, err = server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	require.Equal(t, wantInstructions, session.InitializeResult().Instructions)
}

// TestCreateServer_EmptyInstructionsDoesNotBreakCreation ensures an empty
// instructions string (bypassing domain.Validate, which normally requires
// it) does not prevent server creation or client initialization.
func TestCreateServer_EmptyInstructionsDoesNotBreakCreation(t *testing.T) {
	metadata := domain.McpMetadata{
		Server: domain.ServerMetadata{
			Name:         "test-server",
			Version:      "1.0.0",
			Instructions: "",
		},
		Tools: []domain.ToolMetadata{
			{Name: "search", Description: "Search tool"},
			{Name: "read", Description: "Read tool"},
		},
	}

	resourceProvider, err := resources.NewResourceProvider([]resources.ResourceDefinition{}, nil)
	require.NoError(t, err)
	promptProvider := prompts.NewPromptProvider([]prompts.PromptDefinition{}, nil)

	server := CreateServer(metadata, resourceProvider, promptProvider, &mockSearcher{}, config.SearchSettings{MaxResults: 10, ResultMode: config.SearchResultModeReferences})
	require.NotNil(t, server)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	_, err = server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	require.Empty(t, session.InitializeResult().Instructions)
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
