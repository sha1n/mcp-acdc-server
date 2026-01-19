package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sha1n/mcp-acdc-server-go/internal/search"
)

type MockSearcher struct {
	MockSearch func(queryStr string, limit *int) ([]search.SearchResult, error)
}

func (m *MockSearcher) Search(queryStr string, limit *int) ([]search.SearchResult, error) {
	if m.MockSearch != nil {
		return m.MockSearch(queryStr, limit)
	}
	return nil, nil
}
func (m *MockSearcher) IndexDocuments(d []search.Document) error { return nil }
func (m *MockSearcher) Close()                                   {}

func TestSearchToolHandler(t *testing.T) {
	tests := []struct {
		name        string
		args        map[string]interface{}
		mockResults []search.SearchResult
		mockError   error
		wantFound   bool
		wantContent string
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
			wantFound:   true,
			wantContent: "file:///test.md",
		},
		{
			name: "Search miss",
			args: map[string]interface{}{
				"query": "unicorn",
			},
			mockResults: []search.SearchResult{},
			wantFound:   false,
			wantContent: "No results found",
		},
		{
			name: "Empty query",
			args: map[string]interface{}{
				"query": "",
			},
			mockResults: []search.SearchResult{},
			wantFound:   false,
			wantContent: "No results found",
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
			mock := &MockSearcher{
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

			res, err := handler(context.Background(), req)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if len(res.Content) == 0 {
				t.Error("Expected content results")
				return
			}

			c, ok := res.Content[0].(mcp.TextContent)
			if !ok {
				t.Fatalf("Expected TextContent, got %T", res.Content[0])
			}

			if tt.wantFound {
				if !strings.Contains(c.Text, tt.wantContent) {
					t.Errorf("Expected content to contain %q, got: %q", tt.wantContent, c.Text)
				}
			} else {
				if !strings.Contains(c.Text, tt.wantContent) {
					t.Errorf("Expected content to contain %q, got: %q", tt.wantContent, c.Text)
				}
			}
		})
	}
}
