package search

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/stretchr/testify/require"
)

type mockBatchIndexer struct {
	realIndex bleve.Index
	batchErr  error
}

func (m *mockBatchIndexer) NewBatch() *bleve.Batch { return m.realIndex.NewBatch() }

func (m *mockBatchIndexer) Batch(batch *bleve.Batch) error {
	if m.batchErr != nil {
		return m.batchErr
	}
	return m.realIndex.Batch(batch)
}

func testSettings() config.SearchSettings {
	return config.SearchSettings{
		InMemory:      true,
		MaxResults:    10,
		KeywordsBoost: 3,
		NameBoost:     2,
		ContentBoost:  1,
	}
}

func indexChunks(t *testing.T, service *Service, chunks []domain.Chunk) {
	t.Helper()
	stream := make(chan domain.Chunk, len(chunks))
	for _, chunk := range chunks {
		stream <- chunk
	}
	close(stream)
	require.NoError(t, service.Index(context.Background(), stream))
}

func TestSearch_ChunkFieldBoosts(t *testing.T) {
	tests := []struct {
		name     string
		stronger domain.Chunk
		weaker   domain.Chunk
	}{
		{name: "keyword over heading", stronger: domain.Chunk{Keywords: []string{"oauth"}}, weaker: domain.Chunk{HeadingPath: []string{"oauth"}}},
		{name: "heading over title", stronger: domain.Chunk{HeadingPath: []string{"oauth"}}, weaker: domain.Chunk{SourceTitle: "oauth"}},
		{name: "title over path label", stronger: domain.Chunk{SourceTitle: "oauth"}, weaker: domain.Chunk{PathLabels: []string{"oauth"}}},
		{name: "path label over body", stronger: domain.Chunk{PathLabels: []string{"oauth"}}, weaker: domain.Chunk{Content: "oauth"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewService(testSettings())
			defer service.Close()

			stronger := tt.stronger
			stronger.ID = "stronger"
			stronger.SourceID = "stronger-source"
			stronger.SourceURI = "acdc://stronger"
			stronger.ChunkURI = "acdc://stronger#chunk"
			if stronger.Content == "" {
				stronger.Content = "unrelated"
			}
			weaker := tt.weaker
			weaker.ID = "weaker"
			weaker.SourceID = "weaker-source"
			weaker.SourceURI = "acdc://weaker"
			weaker.ChunkURI = "acdc://weaker#chunk"
			if weaker.Content == "" {
				weaker.Content = "unrelated"
			}
			indexChunks(t, service, []domain.Chunk{weaker, stronger})

			results, err := service.Search("oauth", 10)
			require.NoError(t, err)
			require.Len(t, results, 2)
			require.Equal(t, "stronger", results[0].ChunkID)
		})
	}
}

func TestSearch_StoresChunkProvenanceAndSnippet(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()
	chunk := domain.Chunk{
		ID: "guide#oauth", SourceID: "guide", SourceURI: "acdc://guide", ChunkURI: "acdc://guide#oauth",
		SourceTitle: "OAuth guide", SourcePath: "guides/oauth.md", HeadingPath: []string{"Authentication", "OAuth"},
		PathLabels: []string{"guides", "security"}, StartLine: 12, EndLine: 20,
		Content: "Configure OAuth clients with a redirect URI.",
	}
	indexChunks(t, service, []domain.Chunk{chunk})

	results, err := service.Search("redirect", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, SearchResult{
		ChunkID: "guide#oauth", SourceID: "guide", SourceURI: "acdc://guide", ChunkURI: "acdc://guide#oauth",
		SourceTitle: "OAuth guide", SourcePath: "guides/oauth.md", HeadingPath: []string{"Authentication", "OAuth"},
		StartLine: 12, EndLine: 20, Content: chunk.Content,
	}, withoutScoreAndSnippet(results[0]))
	require.Contains(t, results[0].Snippet, "redirect")
}

func withoutScoreAndSnippet(result SearchResult) SearchResult {
	result.Score = 0
	result.Snippet = ""
	return result
}

func TestSearch_AccuracyAndCandidateLimit(t *testing.T) {
	settings := testSettings()
	settings.MaxResults = 2
	service := NewService(settings)
	defer service.Close()
	indexChunks(t, service, []domain.Chunk{
		{ID: "one", SourceID: "source", SourceURI: "acdc://source", ChunkURI: "acdc://source#one", Content: "Searching configuration"},
		{ID: "two", SourceID: "source", SourceURI: "acdc://source", ChunkURI: "acdc://source#two", Content: "Another configuration"},
		{ID: "three", SourceID: "source", SourceURI: "acdc://source", ChunkURI: "acdc://source#three", Content: "Third configuration"},
	})

	for _, query := range []string{"search", "serch", "*"} {
		results, err := service.Search(query, 0)
		require.NoError(t, err)
		if query == "*" {
			require.Len(t, results, 2)
		} else {
			require.NotEmpty(t, results)
		}
	}
	results, err := service.Search("configuration", 1)
	require.NoError(t, err)
	require.Len(t, results, 1)
	results, err = service.Search("*", -1)
	require.NoError(t, err)
	require.Len(t, results, 2)
}

func TestSearch_DeterministicTieOrdering(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()
	indexChunks(t, service, []domain.Chunk{
		{ID: "b", SourceID: "source-b", SourceURI: "acdc://b", ChunkURI: "acdc://b#b", Content: "identical"},
		{ID: "c", SourceID: "source-a", SourceURI: "acdc://a", ChunkURI: "acdc://a#c", Content: "identical"},
		{ID: "a", SourceID: "source-a", SourceURI: "acdc://a", ChunkURI: "acdc://a#a", Content: "identical"},
	})

	results, err := service.Search("identical", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "c", "b"}, chunkIDs(results))
}

func TestSearch_MissingStoredFieldsUsesStableDefaults(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()
	index, err := bleve.NewMemOnly(buildMapping())
	require.NoError(t, err)
	require.NoError(t, index.Index("fallback-id", struct {
		Content string `json:"content"`
	}{Content: "visible"}))
	service.index = index

	results, err := service.Search("visible", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "fallback-id", results[0].ChunkID)
	require.Empty(t, results[0].SourceURI)
}

func TestSearch_EmptyIndex(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()

	results, err := service.Search("anything", 10)
	require.NoError(t, err)
	require.Empty(t, results)
}

func TestService_Index_ContextCancellation(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, service.Index(ctx, make(chan domain.Chunk)), context.Canceled)
}

func TestService_BatchIndexFailures(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()
	index, err := bleve.NewMemOnly(buildMapping())
	require.NoError(t, err)

	t.Run("rejects missing chunk id", func(t *testing.T) {
		stream := make(chan domain.Chunk, 1)
		stream <- domain.Chunk{Content: "invalid"}
		close(stream)
		require.ErrorContains(t, service.batchIndex(context.Background(), index, stream), "failed to add chunk to batch")
	})
	t.Run("reports final batch failure", func(t *testing.T) {
		stream := make(chan domain.Chunk, 1)
		stream <- domain.Chunk{ID: "chunk", Content: "valid"}
		close(stream)
		mock := &mockBatchIndexer{realIndex: index, batchErr: errors.New("batch failed")}
		require.ErrorContains(t, service.batchIndex(context.Background(), mock, stream), "failed to execute final batch index")
	})
	t.Run("reports full batch failure", func(t *testing.T) {
		stream := make(chan domain.Chunk, 100)
		for i := 0; i < 100; i++ {
			stream <- domain.Chunk{ID: fmt.Sprintf("chunk-%d", i), Content: "valid"}
		}
		close(stream)
		mock := &mockBatchIndexer{realIndex: index, batchErr: errors.New("batch failed")}
		require.ErrorContains(t, service.batchIndex(context.Background(), mock, stream), "failed to execute batch index")
	})
}

func TestService_IndexRebuildsAndBatchesChunks(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()
	chunks := make([]domain.Chunk, 150)
	for i := range chunks {
		chunks[i] = domain.Chunk{ID: fmt.Sprintf("chunk-%d", i), SourceID: "source", SourceURI: "acdc://source", ChunkURI: fmt.Sprintf("acdc://source#%d", i), Content: "content"}
	}
	indexChunks(t, service, chunks)
	count, err := service.DocCount()
	require.NoError(t, err)
	require.Equal(t, uint64(len(chunks)), count)
	indexChunks(t, service, []domain.Chunk{{ID: "replacement", SourceID: "new", SourceURI: "acdc://new", ChunkURI: "acdc://new#replacement", Content: "new"}})
	count, err = service.DocCount()
	require.NoError(t, err)
	require.Equal(t, uint64(1), count)
}

func TestService_ReplaceAndDeleteSource(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()
	indexChunks(t, service, []domain.Chunk{{ID: "old", SourceID: "source", SourceURI: "acdc://source", ChunkURI: "acdc://source#old", Content: "legacy"}})

	require.NoError(t, service.ReplaceSource(context.Background(), "source", []domain.Chunk{{ID: "new", SourceID: "source", SourceURI: "acdc://source", ChunkURI: "acdc://source#new", Content: "modern"}}))
	oldResults, err := service.Search("legacy", 10)
	require.NoError(t, err)
	require.Empty(t, oldResults)
	newResults, err := service.Search("modern", 10)
	require.NoError(t, err)
	require.Len(t, newResults, 1)

	require.NoError(t, service.DeleteSource(context.Background(), "source"))
	newResults, err = service.Search("modern", 10)
	require.NoError(t, err)
	require.Empty(t, newResults)
}

func TestService_ReplaceSource_InvalidChunkPreservesExistingSource(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()
	indexChunks(t, service, []domain.Chunk{{ID: "old", SourceID: "source", SourceURI: "acdc://source", ChunkURI: "acdc://source#old", Content: "legacy"}})

	err := service.ReplaceSource(context.Background(), "source", []domain.Chunk{{Content: "invalid"}})
	require.ErrorContains(t, err, "failed to add replacement chunk to batch")
	results, err := service.Search("legacy", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "old", results[0].ChunkID)
}

func chunkIDs(results []SearchResult) []string {
	ids := make([]string, len(results))
	for i, result := range results {
		ids[i] = result.ChunkID
	}
	return ids
}
