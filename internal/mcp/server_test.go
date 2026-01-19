package mcp

import (
	"context"
	"os"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sha1n/mcp-acdc-server-go/internal/domain"
	"github.com/sha1n/mcp-acdc-server-go/internal/resources"
)

func TestCreateServer(t *testing.T) {
	// Setup dependencies
	serverMeta := domain.ServerMetadata{
		Name:         "test-server",
		Version:      "1.0.0",
		Instructions: "Run tests",
	}
	metadata := domain.McpMetadata{
		Server: serverMeta,
		Tools: []domain.ToolMetadata{
			{
				Name:        "search",
				Description: "Search tool",
			},
		},
	}

	// Mock or create real providers
	// For resources, we need a simple content provider simulation if we want to be pure.
	// But `resources.ResourceProvider` takes a list of `domain.ResourceDefinition`.

	resDefs := []resources.ResourceDefinition{
		{
			URI:         "file:///path/to/res",
			Name:        "test-resource",
			Description: "desc",
			MIMEType:    "text/plain",
		},
	}
	resProvider := resources.NewResourceProvider(resDefs)

	searchService := &MockSearcher{}

	// Create server
	s := CreateServer(metadata, resProvider, searchService)

	if s == nil {
		t.Fatal("CreateServer returned nil")
	}

	// Basic validation of server creation
	// Since we can't easily inspect internal state without access,
	// we assume if CreateServer didn't panic and returned non-nil, it's a pass for this level.
	// We can try to register a tool that conflicts or something, but `mcp-go` might not error until runtime.

	// Let's at least check that we can add a new tool, which implies `s` is initialized.
	s.AddTool(mcp.NewTool("dummy", mcp.WithDescription("dummy")), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("dummy"), nil
	})

	// How to verify registration?
	// We can try to define a test case that mimics the server's internal state check if exposed,
	// or more robustly, we can check if the server Name/Version are set correctly (if exposed via getter).
	// mcp-go MCPServer struct fields are private usually?
	// Actually NewMCPServer returns *MCPServer.

	// A good check is to try to AddTool with the same name and expect error?
	// Or just trust that if it didn't panic, it's mostly fine.

	// But let's go a step further and try to call the resource handler.
	// Since `CreateServer` registers a handler for `resDefs`.
	// The handler is a closure.

	// NOTE: Because we can't easily access the internal mux of `mcp-go` server,
	// we are limited to black-box testing unless we use `ServeStdio` style loop.
	// However, ensuring it constructs without panic is a good start for unit test.
	// The integration tests will cover the actual interface response.
}

func TestResourceHandler(t *testing.T) {
	// Setup temporary content
	tempDir := t.TempDir()
	resFile := tempDir + "/res.md"
	_ = os.WriteFile(resFile, []byte("---\nname: res\n---\ncontent"), 0644)

	resDefs := []resources.ResourceDefinition{
		{URI: "file:///res", Name: "res", FilePath: resFile},
	}
	provider := resources.NewResourceProvider(resDefs)

	handler := makeResourceHandler(provider, "file:///res")

	// Test success
	contents, err := handler(context.Background(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("Handler failed: %v", err)
	}
	if len(contents) != 1 {
		t.Fatalf("Expected 1 content, got %d", len(contents))
	}
	text, ok := contents[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatal("Expected TextResourceContents")
	}
	if text.Text != "content" {
		t.Errorf("Expected 'content', got '%s'", text.Text)
	}

	// Test failure (e.g. file removed)
	_ = os.Remove(resFile)
	_, err = handler(context.Background(), mcp.ReadResourceRequest{})
	if err == nil {
		t.Error("Expected error when file is missing, got nil")
	}
}
