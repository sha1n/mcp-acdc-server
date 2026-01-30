package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock searcher for testing
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

func (m *TestMockSearcher) Index(ctx context.Context, docs <-chan domain.Document) error {
	for range docs {
		// drain
	}
	return nil
}

func TestToolRegistration(t *testing.T) {
	// Just verify tools can be created without panic
	mockSearcher := &TestMockSearcher{}
	searchHandler := NewSearchToolHandler(mockSearcher)
	if searchHandler == nil {
		t.Error("Search handler should not be nil")
	}

	resourceProvider := resources.NewResourceProvider([]resources.ResourceDefinition{})
	readHandler := NewReadToolHandler(resourceProvider)
	if readHandler == nil {
		t.Error("Read handler should not be nil")
	}
}

func TestSearchToolHandler_Success_WithResults(t *testing.T) {
	mockSearcher := &TestMockSearcher{
		MockSearch: func(query string, limit *int) ([]search.SearchResult, error) {
			assert.Equal(t, "test query", query)
			return []search.SearchResult{
				{
					Name:    "Result 1",
					URI:     "acdc://result1",
					Snippet: "This is result 1",
				},
				{
					Name:    "Result 2",
					URI:     "acdc://result2",
					Snippet: "This is result 2",
				},
			}, nil
		},
	}

	handler := NewSearchToolHandler(mockSearcher)
	require.NotNil(t, handler)

	ctx := context.Background()
	req := &mcp.CallToolRequest{}
	args := SearchToolArgument{Query: "test query"}

	result, extra, err := handler(ctx, req, args)

	require.NoError(t, err)
	require.Nil(t, extra)
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)

	textContent, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, textContent.Text, "Search results for 'test query'")
	assert.Contains(t, textContent.Text, "Result 1")
	assert.Contains(t, textContent.Text, "acdc://result1")
	assert.Contains(t, textContent.Text, "This is result 1")
	assert.Contains(t, textContent.Text, "Result 2")
}

func TestSearchToolHandler_Success_NoResults(t *testing.T) {
	mockSearcher := &TestMockSearcher{
		MockSearch: func(query string, limit *int) ([]search.SearchResult, error) {
			return []search.SearchResult{}, nil
		},
	}

	handler := NewSearchToolHandler(mockSearcher)
	ctx := context.Background()
	req := &mcp.CallToolRequest{}
	args := SearchToolArgument{Query: "nonexistent"}

	result, extra, err := handler(ctx, req, args)

	require.NoError(t, err)
	require.Nil(t, extra)
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)

	textContent, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, textContent.Text, "No results found for 'nonexistent'")
}

func TestSearchToolHandler_Error(t *testing.T) {
	expectedErr := errors.New("search service error")
	mockSearcher := &TestMockSearcher{
		MockSearch: func(query string, limit *int) ([]search.SearchResult, error) {
			return nil, expectedErr
		},
	}

	handler := NewSearchToolHandler(mockSearcher)
	ctx := context.Background()
	req := &mcp.CallToolRequest{}
	args := SearchToolArgument{Query: "failing query"}

	result, extra, err := handler(ctx, req, args)

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, result)
	assert.Nil(t, extra)
}

func TestReadToolHandler_Success(t *testing.T) {
	// Create temp file with markdown content
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test-resource.md")
	resourceContent := "---\nname: Test Resource\ndescription: A test\n---\n# Test Content\n\nThis is test content."
	err := os.WriteFile(filePath, []byte(resourceContent), 0644)
	require.NoError(t, err)

	resourceProvider := resources.NewResourceProvider([]resources.ResourceDefinition{
		{
			Name:        "Test Resource",
			URI:         "acdc://test-resource",
			Description: "A test resource",
			MIMEType:    "text/markdown",
			FilePath:    filePath,
		},
	})

	handler := NewReadToolHandler(resourceProvider)
	require.NotNil(t, handler)

	ctx := context.Background()
	req := &mcp.CallToolRequest{}
	args := ReadToolArgument{URI: "acdc://test-resource"}

	result, extra, err := handler(ctx, req, args)

	require.NoError(t, err)
	require.Nil(t, extra)
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)

	textContent, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "# Test Content\n\nThis is test content.", textContent.Text)
}

func TestReadToolHandler_Error_ResourceNotFound(t *testing.T) {
	resourceProvider := resources.NewResourceProvider([]resources.ResourceDefinition{})

	handler := NewReadToolHandler(resourceProvider)
	ctx := context.Background()
	req := &mcp.CallToolRequest{}
	args := ReadToolArgument{URI: "acdc://nonexistent"}

	result, extra, err := handler(ctx, req, args)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "resource")
	assert.Nil(t, result)
	assert.Nil(t, extra)
}
