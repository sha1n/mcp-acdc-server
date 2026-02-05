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
	MockSearch func(queryStr string, opts *search.SearchOptions) ([]search.SearchResult, error)
}

func (m *TestMockSearcher) Search(query string, opts *search.SearchOptions) ([]search.SearchResult, error) {
	if m.MockSearch != nil {
		return m.MockSearch(query, opts)
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
		MockSearch: func(query string, opts *search.SearchOptions) ([]search.SearchResult, error) {
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
		MockSearch: func(query string, opts *search.SearchOptions) ([]search.SearchResult, error) {
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
		MockSearch: func(query string, opts *search.SearchOptions) ([]search.SearchResult, error) {
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

func TestSearchToolHandler_WithSourceFilter(t *testing.T) {
	mockSearcher := &TestMockSearcher{
		MockSearch: func(query string, opts *search.SearchOptions) ([]search.SearchResult, error) {
			assert.Equal(t, "test query", query)
			require.NotNil(t, opts)
			assert.Equal(t, "docs", opts.Source)
			return []search.SearchResult{
				{
					Name:    "Doc Result",
					URI:     "acdc://docs/result",
					Snippet: "This is a docs result",
					Source:  "docs",
				},
			}, nil
		},
	}

	handler := NewSearchToolHandler(mockSearcher)
	ctx := context.Background()
	req := &mcp.CallToolRequest{}
	args := SearchToolArgument{Query: "test query", Source: "docs"}

	result, extra, err := handler(ctx, req, args)

	require.NoError(t, err)
	require.Nil(t, extra)
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)

	textContent, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, textContent.Text, "Search results for 'test query' in source 'docs'")
	assert.Contains(t, textContent.Text, "[docs]")
	assert.Contains(t, textContent.Text, "Doc Result")
}

func TestSearchToolHandler_WithSourceFilter_NoResults(t *testing.T) {
	mockSearcher := &TestMockSearcher{
		MockSearch: func(query string, opts *search.SearchOptions) ([]search.SearchResult, error) {
			require.NotNil(t, opts)
			assert.Equal(t, "internal", opts.Source)
			return []search.SearchResult{}, nil
		},
	}

	handler := NewSearchToolHandler(mockSearcher)
	ctx := context.Background()
	req := &mcp.CallToolRequest{}
	args := SearchToolArgument{Query: "test query", Source: "internal"}

	result, extra, err := handler(ctx, req, args)

	require.NoError(t, err)
	require.Nil(t, extra)
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)

	textContent, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, textContent.Text, "No results found for 'test query' in source 'internal'")
}

func TestSearchToolHandler_EmptySourceNotPassedAsFilter(t *testing.T) {
	mockSearcher := &TestMockSearcher{
		MockSearch: func(query string, opts *search.SearchOptions) ([]search.SearchResult, error) {
			// Empty source should result in nil opts (no filter)
			assert.Nil(t, opts)
			return []search.SearchResult{
				{
					Name:    "Result",
					URI:     "acdc://docs/result",
					Snippet: "This is a result",
					Source:  "docs",
				},
			}, nil
		},
	}

	handler := NewSearchToolHandler(mockSearcher)
	ctx := context.Background()
	req := &mcp.CallToolRequest{}
	args := SearchToolArgument{Query: "test query", Source: ""}

	result, extra, err := handler(ctx, req, args)

	require.NoError(t, err)
	require.Nil(t, extra)
	require.NotNil(t, result)
}

func TestSearchToolHandler_ResultsWithSource(t *testing.T) {
	mockSearcher := &TestMockSearcher{
		MockSearch: func(query string, opts *search.SearchOptions) ([]search.SearchResult, error) {
			return []search.SearchResult{
				{
					Name:    "Doc 1",
					URI:     "acdc://docs/doc1",
					Snippet: "Documentation snippet",
					Source:  "docs",
				},
				{
					Name:    "Internal 1",
					URI:     "acdc://internal/int1",
					Snippet: "Internal snippet",
					Source:  "internal",
				},
			}, nil
		},
	}

	handler := NewSearchToolHandler(mockSearcher)
	ctx := context.Background()
	req := &mcp.CallToolRequest{}
	args := SearchToolArgument{Query: "test"}

	result, extra, err := handler(ctx, req, args)

	require.NoError(t, err)
	require.Nil(t, extra)
	require.NotNil(t, result)

	textContent, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	// Verify source prefixes in output
	assert.Contains(t, textContent.Text, "[docs]")
	assert.Contains(t, textContent.Text, "[internal]")
}
