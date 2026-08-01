package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock searcher for testing
type TestMockSearcher struct {
	results        []search.SearchResult
	err            error
	query          string
	candidateLimit int
}

func (m *TestMockSearcher) Search(query string, candidateLimit int) ([]search.SearchResult, error) {
	m.query = query
	m.candidateLimit = candidateLimit
	return m.results, m.err
}

func (m *TestMockSearcher) Close() {}

func (m *TestMockSearcher) Index(ctx context.Context, chunks <-chan domain.Chunk) error {
	for range chunks {
		// drain
	}
	return nil
}

func (m *TestMockSearcher) ReplaceSource(context.Context, string, []domain.Chunk) error { return nil }
func (m *TestMockSearcher) DeleteSource(context.Context, string) error                  { return nil }

func TestToolRegistration(t *testing.T) {
	// Just verify tools can be created without panic
	mockSearcher := &TestMockSearcher{}
	searchHandler := NewSearchToolHandler(mockSearcher, config.SearchSettings{})
	if searchHandler == nil {
		t.Error("Search handler should not be nil")
	}

	resourceProvider, err := resources.NewResourceProvider([]resources.ResourceDefinition{}, nil)
	require.NoError(t, err)
	readHandler := NewReadToolHandler(resourceProvider)
	if readHandler == nil {
		t.Error("Read handler should not be nil")
	}
}

func TestToolArgumentSchemas_ContainOnlyQueryAndURI(t *testing.T) {
	require.Equal(t, []string{"Query"}, publicFieldNames(reflect.TypeFor[SearchToolArgument]()))
	require.Equal(t, []string{"URI"}, publicFieldNames(reflect.TypeFor[ReadToolArgument]()))
}

func TestSearchToolHandler_Success_WithResults(t *testing.T) {
	mockSearcher := &TestMockSearcher{
		results: []search.SearchResult{
			{
				SourceID:    "result1",
				SourceTitle: "Result 1",
				ChunkURI:    "acdc://result1#chunk",
				Snippet:     "This is result 1",
			},
			{
				SourceID:    "result2",
				SourceTitle: "Result 2",
				ChunkURI:    "acdc://result2#chunk",
				Snippet:     "This is result 2",
			},
		},
	}

	handler := NewSearchToolHandler(mockSearcher, config.SearchSettings{MaxResults: 10, ResultMode: config.SearchResultModeReferences})
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
	assert.Contains(t, textContent.Text, "acdc://result1#chunk")
	assert.Contains(t, textContent.Text, "This is result 1")
	assert.Contains(t, textContent.Text, "Result 2")
	assert.Equal(t, "test query", mockSearcher.query)
}

func TestSearchToolHandler_UsesServerWideModeAndCandidateWindow(t *testing.T) {
	searcher := &TestMockSearcher{results: []search.SearchResult{{
		ChunkID:     "one",
		SourceID:    "one",
		ChunkURI:    "acdc://one#document",
		SourceTitle: "One",
		Content:     "full chunk",
	}}}
	handler := NewSearchToolHandler(searcher, config.SearchSettings{MaxResults: 10, ResultMode: config.SearchResultModeContent})

	result, _, err := handler(context.Background(), nil, SearchToolArgument{Query: "chunk"})

	require.NoError(t, err)
	require.Equal(t, 50, searcher.candidateLimit)
	require.Contains(t, result.Content[0].(*mcp.TextContent).Text, "full chunk")
}

func TestSearchToolHandler_Success_NoResults(t *testing.T) {
	mockSearcher := &TestMockSearcher{}

	handler := NewSearchToolHandler(mockSearcher, config.SearchSettings{MaxResults: 10, ResultMode: config.SearchResultModeReferences})
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
		err: expectedErr,
	}

	handler := NewSearchToolHandler(mockSearcher, config.SearchSettings{MaxResults: 10, ResultMode: config.SearchResultModeReferences})
	ctx := context.Background()
	req := &mcp.CallToolRequest{}
	args := SearchToolArgument{Query: "failing query"}

	result, extra, err := handler(ctx, req, args)

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, result)
	assert.Nil(t, extra)
}

func publicFieldNames(t reflect.Type) []string {
	fieldNames := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.IsExported() {
			fieldNames = append(fieldNames, field.Name)
		}
	}
	return fieldNames
}

func TestReadToolHandler_Success(t *testing.T) {
	// Create temp file with markdown content
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test-resource.md")
	resourceContent := "---\nname: Test Resource\ndescription: A test\n---\n# Test Content\n\nThis is test content."
	err := os.WriteFile(filePath, []byte(resourceContent), 0644)
	require.NoError(t, err)

	resourceProvider, err := resources.NewResourceProvider([]resources.ResourceDefinition{
		{
			Name:        "Test Resource",
			URI:         "acdc://test-resource",
			Description: "A test resource",
			MIMEType:    "text/markdown",
			FilePath:    filePath,
			Content:     "# Test Content\n\nThis is test content.",
		},
	}, nil)
	require.NoError(t, err)

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
	resourceProvider, err := resources.NewResourceProvider([]resources.ResourceDefinition{}, nil)
	require.NoError(t, err)

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
