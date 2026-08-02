package mcp

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/stretchr/testify/require"
)

// tracingRevalidator records that Revalidate ran onto a trace shared with a
// tracingCatalog or tracingSearcher, so a test can assert the revalidation
// happens strictly before the catalog or searcher is touched.
type tracingRevalidator struct {
	trace *[]string
}

func (r tracingRevalidator) Revalidate(context.Context) {
	*r.trace = append(*r.trace, "revalidate")
}

// tracingCatalog is a minimal resources.Catalog that records reads onto the
// trace a tracingRevalidator writes to.
type tracingCatalog struct {
	trace   *[]string
	content string
}

func (c tracingCatalog) ListResources() []mcp.Resource { return nil }

func (c tracingCatalog) ReadResource(string) (string, error) {
	*c.trace = append(*c.trace, "catalog.ReadResource")
	return c.content, nil
}

func (c tracingCatalog) StreamChunks(_ context.Context, ch chan<- domain.Chunk) error {
	close(ch)
	return nil
}

func (c tracingCatalog) StreamResources(_ context.Context, ch chan<- domain.Document) error {
	close(ch)
	return nil
}

// tracingSearcher is a minimal search.Searcher that records searches onto the
// trace a tracingRevalidator writes to.
type tracingSearcher struct {
	trace *[]string
}

func (s tracingSearcher) Search(string, int) ([]search.SearchResult, error) {
	*s.trace = append(*s.trace, "searcher.Search")
	return nil, nil
}

func (s tracingSearcher) Close() {}

func (s tracingSearcher) Index(_ context.Context, chunks <-chan domain.Chunk) error {
	for range chunks {
		// drain
	}
	return nil
}

func TestMakeResourceHandler_RevalidatesBeforeCatalogAccess(t *testing.T) {
	var trace []string
	catalog := tracingCatalog{trace: &trace, content: "body"}
	revalidator := tracingRevalidator{trace: &trace}

	handler := makeResourceHandler(catalog, revalidator, "acdc://doc")
	_, err := handler(context.Background(), &mcp.ReadResourceRequest{
		Params: &mcp.ReadResourceParams{URI: "acdc://doc"},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"revalidate", "catalog.ReadResource"}, trace)
}

func TestReadToolHandler_RevalidatesBeforeCatalogAccess(t *testing.T) {
	var trace []string
	catalog := tracingCatalog{trace: &trace, content: "body"}
	revalidator := tracingRevalidator{trace: &trace}

	handler := NewReadToolHandler(catalog, revalidator)
	_, _, err := handler(context.Background(), &mcp.CallToolRequest{}, ReadToolArgument{URI: "acdc://doc"})

	require.NoError(t, err)
	require.Equal(t, []string{"revalidate", "catalog.ReadResource"}, trace)
}

func TestSearchToolHandler_RevalidatesBeforeSearcherAccess(t *testing.T) {
	var trace []string
	searcher := tracingSearcher{trace: &trace}
	revalidator := tracingRevalidator{trace: &trace}

	handler := NewSearchToolHandler(searcher, revalidator, config.SearchSettings{MaxResults: 10, ResultMode: config.SearchResultModeReferences})
	_, _, err := handler(context.Background(), &mcp.CallToolRequest{}, SearchToolArgument{Query: "test"})

	require.NoError(t, err)
	require.Equal(t, []string{"revalidate", "searcher.Search"}, trace)
}

func TestNoopRevalidator_DoesNothing(t *testing.T) {
	require.NotPanics(t, func() {
		noopRevalidator{}.Revalidate(context.Background())
	})
}
