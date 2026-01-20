package mcp

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sha1n/mcp-acdc-server-go/internal/resources"
	"github.com/sha1n/mcp-acdc-server-go/internal/search"
)

// Define a local mock that allows customizing search behavior
type TestMockSearcher struct {
	MockSearch func(queryStr string, limit *int) ([]search.SearchResult, error)
}

func (m *TestMockSearcher) Search(query string, options *int) ([]search.SearchResult, error) {
	if m.MockSearch != nil {
		return m.MockSearch(query, options)
	}
	return nil, nil
}

func (m *TestMockSearcher) Close() {}

func (m *TestMockSearcher) IndexDocuments(docs []search.Document) error {
	return nil
}

func TestGetResourceToolHandler_Errors(t *testing.T) {
	provider := resources.NewResourceProvider(nil)
	handler := NewGetResourceToolHandler(provider)

	t.Run("MissingArguments", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "get_resource",
				Arguments: map[string]interface{}{},
			},
		}
		_, err := handler(context.Background(), req)
		if err == nil {
			t.Error("expected error for missing 'uri' argument")
		}
	})

	t.Run("UnknownResource", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "get_resource",
				Arguments: map[string]interface{}{
					"uri": "acdc://unknown",
				},
			},
		}
		_, err := handler(context.Background(), req)
		if err == nil {
			t.Error("expected error for unknown resource")
		}
	})
}

func TestSearchToolHandler(t *testing.T) {
	tests := []struct {
		name        string
		args        map[string]interface{}
		mockResults []search.SearchResult
		mockError   error
		wantResult  string
		expectError bool
	}{
		{
			name: "Search hit",
			args: map[string]interface{}{
				"query": "fox",
			},
			mockResults: []search.SearchResult{
				{URI: "file:///test.md", Name: "Test Doc", Snippet: "snippet"},
			},
			wantResult: "Search results for 'fox':\n\n- [Test Doc](file:///test.md): snippet\n\n",
		},
		{
			name: "Search miss",
			args: map[string]interface{}{
				"query": "unicorn",
			},
			mockResults: []search.SearchResult{},
			wantResult:  "No results found for 'unicorn'",
		},
		{
			name: "Empty query",
			args: map[string]interface{}{
				"query": "",
			},
			mockResults: []search.SearchResult{},
			expectError: true,
		},
		{
			name:        "Missing argument",
			args:        map[string]interface{}{},
			expectError: true,
		},
		{
			name:        "Invalid argument format",
			args:        nil, // Not a map[string]interface{}
			expectError: true,
		},
		{
			name: "Search error",
			args: map[string]interface{}{
				"query": "boom",
			},
			mockError:   context.DeadlineExceeded, // Generic error
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &TestMockSearcher{
				MockSearch: func(queryStr string, limit *int) ([]search.SearchResult, error) {
					return tt.mockResults, tt.mockError
				},
			}
			handler := NewSearchToolHandler(mock)

			req := mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name:      "search",
					Arguments: tt.args,
				},
			}

			resp, err := handler(context.Background(), req)

			if tt.expectError {
				if err == nil {
					t.Errorf("%s: expected error, got nil", tt.name)
				}
				return
			}

			if err != nil {
				t.Fatalf("%s: unexpected error: %v", tt.name, err)
			}

			if len(resp.Content) != 1 {
				t.Fatalf("%s: expected 1 content item, got %d", tt.name, len(resp.Content))
			}

			text, ok := resp.Content[0].(mcp.TextContent)
			if !ok {
				t.Fatalf("%s: expected TextContent, got %T", tt.name, resp.Content[0])
			}

			if text.Text != tt.wantResult {
				t.Errorf("%s:\nwant: %q\ngot : %q", tt.name, tt.wantResult, text.Text)
			}
		})
	}
}
