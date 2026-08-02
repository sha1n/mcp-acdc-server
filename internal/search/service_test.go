package search

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/stretchr/testify/require"
)

type mockBatchIndexer struct {
	realIndex bleve.Index
	batchErr  error
}

type failingSearchIndex struct {
	backing     bleve.Index
	batchErr    error
	searchErr   error
	docCountErr error
}

func (i *failingSearchIndex) NewBatch() *bleve.Batch { return i.backing.NewBatch() }

func (i *failingSearchIndex) Batch(batch *bleve.Batch) error {
	if i.batchErr != nil {
		return i.batchErr
	}
	return i.backing.Batch(batch)
}

func (i *failingSearchIndex) Search(request *bleve.SearchRequest) (*bleve.SearchResult, error) {
	if i.searchErr != nil {
		return nil, i.searchErr
	}
	return i.backing.Search(request)
}

func (i *failingSearchIndex) Close() error { return i.backing.Close() }

func (i *failingSearchIndex) DocCount() (uint64, error) {
	if i.docCountErr != nil {
		return 0, i.docCountErr
	}
	return i.backing.DocCount()
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
		TitleBoost:    2,
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

func TestSearch_UsesSourceTitleWhenContentHasNoSnippet(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()
	indexChunks(t, service, []domain.Chunk{{ID: "title", SourceID: "source", SourceURI: "acdc://source", ChunkURI: "acdc://source#title", SourceTitle: "Title fallback"}})

	results, err := service.Search("title", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "Title fallback", results[0].Snippet)
}

func TestSearch_FieldConversions(t *testing.T) {
	fields := map[string]interface{}{
		"strings":     []string{"first", "second"},
		"interfaces":  []interface{}{"first", 1, "second"},
		"single":      "only",
		"unsupported": []int{1},
		"float64":     float64(1),
		"float32":     float32(2),
		"int":         3,
		"int64":       int64(4),
		"no-number":   "five",
	}

	require.Equal(t, []string{"first", "second"}, fieldStrings(fields, "strings"))
	require.Equal(t, []string{"first", "second"}, fieldStrings(fields, "interfaces"))
	require.Equal(t, []string{"only"}, fieldStrings(fields, "single"))
	require.Nil(t, fieldStrings(fields, "unsupported"))
	require.Equal(t, 1, fieldInt(fields, "float64"))
	require.Equal(t, 2, fieldInt(fields, "float32"))
	require.Equal(t, 3, fieldInt(fields, "int"))
	require.Equal(t, 4, fieldInt(fields, "int64"))
	require.Zero(t, fieldInt(fields, "no-number"))
}

func TestService_ReportsIndexOperationFailures(t *testing.T) {
	backing, err := bleve.NewMemOnly(buildMapping())
	require.NoError(t, err)
	service := NewService(testSettings())
	defer service.Close()
	service.index = &failingSearchIndex{backing: backing, searchErr: errors.New("search failed")}

	results, err := service.Search("query", 10)
	require.ErrorContains(t, err, "search failed")
	require.Nil(t, results)
}

func TestService_IndexCreationFailuresPreserveExistingState(t *testing.T) {
	settings := testSettings()
	settings.InMemory = false
	service := NewService(settings)
	defer service.Close()
	indexChunks(t, service, []domain.Chunk{{ID: "stable", SourceID: "source", SourceURI: "acdc://source", ChunkURI: "acdc://source#stable", Content: "stable"}})

	originalMakeTempDir := makeTempDir
	makeTempDir = func(string, string) (string, error) { return "", errors.New("temp failure") }
	t.Cleanup(func() { makeTempDir = originalMakeTempDir })
	chunks := make(chan domain.Chunk)
	close(chunks)
	require.ErrorContains(t, service.Index(context.Background(), chunks), "temp failure")
	results, err := service.Search("stable", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
}

func TestService_NewIndexReportsDependencyFailures(t *testing.T) {
	t.Run("memory index", func(t *testing.T) {
		service := NewService(testSettings())
		originalNewMemoryIndex := newMemoryIndex
		newMemoryIndex = func(mapping.IndexMapping) (bleve.Index, error) { return nil, errors.New("memory failure") }
		t.Cleanup(func() { newMemoryIndex = originalNewMemoryIndex })
		_, _, err := service.newIndex()
		require.ErrorContains(t, err, "memory failure")
	})

	t.Run("remove temporary directory", func(t *testing.T) {
		settings := testSettings()
		settings.InMemory = false
		service := NewService(settings)
		originalMakeTempDir, originalRemoveAll := makeTempDir, removeAll
		makeTempDir = func(string, string) (string, error) { return filepath.Join(t.TempDir(), "index"), nil }
		removeAll = func(string) error { return errors.New("remove failure") }
		t.Cleanup(func() {
			makeTempDir = originalMakeTempDir
			removeAll = originalRemoveAll
		})
		_, _, err := service.newIndex()
		require.ErrorContains(t, err, "remove failure")
	})

	t.Run("create disk index", func(t *testing.T) {
		settings := testSettings()
		settings.InMemory = false
		service := NewService(settings)
		originalNewDiskIndex := newDiskIndex
		newDiskIndex = func(string, mapping.IndexMapping) (bleve.Index, error) { return nil, errors.New("disk failure") }
		t.Cleanup(func() { newDiskIndex = originalNewDiskIndex })
		_, _, err := service.newIndex()
		require.ErrorContains(t, err, "disk failure")
	})
}

func TestService_CloseCleansUpDiskIndexAndDocCount(t *testing.T) {
	settings := testSettings()
	settings.InMemory = false
	service := NewService(settings)
	indexChunks(t, service, []domain.Chunk{{ID: "disk", SourceID: "source", SourceURI: "acdc://source", ChunkURI: "acdc://source#disk", Content: "disk"}})
	indexDir := service.indexDir
	require.DirExists(t, indexDir)

	service.Close()
	require.NoDirExists(t, indexDir)
	count, err := service.DocCount()
	require.NoError(t, err)
	require.Zero(t, count)
	service.Close()
}

func TestService_DiskRebuildCleansUpPrivateAndReplacedIndexes(t *testing.T) {
	settings := testSettings()
	settings.InMemory = false
	service := NewService(settings)
	defer service.Close()
	indexChunks(t, service, []domain.Chunk{{ID: "old", SourceID: "source", SourceURI: "acdc://source", ChunkURI: "acdc://source#old", Content: "old"}})
	oldIndexDir := service.indexDir

	invalidChunks := make(chan domain.Chunk, 1)
	invalidChunks <- domain.Chunk{Content: "invalid"}
	close(invalidChunks)
	require.Error(t, service.Index(context.Background(), invalidChunks))
	require.DirExists(t, oldIndexDir)
	results, err := service.Search("old", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)

	indexChunks(t, service, []domain.Chunk{{ID: "new", SourceID: "source", SourceURI: "acdc://source", ChunkURI: "acdc://source#new", Content: "new"}})
	require.NoDirExists(t, oldIndexDir)
	results, err = service.Search("new", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
}

func TestService_Index_ContextCancellation(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, service.Index(ctx, make(chan domain.Chunk)), context.Canceled)
}

func TestService_Index_FailedRebuildPreservesExistingIndex(t *testing.T) {
	tests := []struct {
		name   string
		stream func(t *testing.T) (context.Context, <-chan domain.Chunk)
	}{
		{
			name: "cancellation",
			stream: func(t *testing.T) (context.Context, <-chan domain.Chunk) {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, make(chan domain.Chunk)
			},
		},
		{
			name: "batch failure",
			stream: func(t *testing.T) (context.Context, <-chan domain.Chunk) {
				t.Helper()
				chunks := make(chan domain.Chunk, 1)
				chunks <- domain.Chunk{Content: "invalid"}
				close(chunks)
				return context.Background(), chunks
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewService(testSettings())
			defer service.Close()
			indexChunks(t, service, []domain.Chunk{{ID: "stable", SourceID: "source", SourceURI: "acdc://source", ChunkURI: "acdc://source#stable", Content: "stable content"}})

			ctx, chunks := tt.stream(t)
			require.Error(t, service.Index(ctx, chunks))
			results, err := service.Search("stable", 10)
			require.NoError(t, err)
			require.Len(t, results, 1)
			require.Equal(t, "stable", results[0].ChunkID)
		})
	}
}

func TestService_BatchIndexFailures(t *testing.T) {
	index, err := bleve.NewMemOnly(buildMapping())
	require.NoError(t, err)

	t.Run("rejects missing chunk id", func(t *testing.T) {
		stream := make(chan domain.Chunk, 1)
		stream <- domain.Chunk{Content: "invalid"}
		close(stream)
		require.ErrorContains(t, batchIndex(context.Background(), index, stream), "failed to add chunk to batch")
	})
	t.Run("reports final batch failure", func(t *testing.T) {
		stream := make(chan domain.Chunk, 1)
		stream <- domain.Chunk{ID: "chunk", Content: "valid"}
		close(stream)
		mock := &mockBatchIndexer{realIndex: index, batchErr: errors.New("batch failed")}
		require.ErrorContains(t, batchIndex(context.Background(), mock, stream), "failed to execute final batch index")
	})
	t.Run("reports full batch failure", func(t *testing.T) {
		stream := make(chan domain.Chunk, 100)
		for i := 0; i < 100; i++ {
			stream <- domain.Chunk{ID: fmt.Sprintf("chunk-%d", i), Content: "valid"}
		}
		close(stream)
		mock := &mockBatchIndexer{realIndex: index, batchErr: errors.New("batch failed")}
		require.ErrorContains(t, batchIndex(context.Background(), mock, stream), "failed to execute batch index")
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

func chunkIDs(results []SearchResult) []string {
	ids := make([]string, len(results))
	for i, result := range results {
		ids[i] = result.ChunkID
	}
	return ids
}
