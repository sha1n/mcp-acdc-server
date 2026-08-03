package search

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	bleveindex "github.com/blevesearch/bleve_index_api"
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
		KeywordsBoost: config.DefaultKeywordsBoost,
		HeadingBoost:  config.DefaultHeadingBoost,
		TitleBoost:    config.DefaultTitleBoost,
		PathBoost:     config.DefaultPathBoost,
		ContentBoost:  config.DefaultContentBoost,
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

// TestSearch_ReadsHeadingAndPathBoostsFromSettings pins that both boosts are
// configuration rather than constants. Zeroing the heading boost must remove the
// heading_path clause from the query, letting a body-only match overtake a
// heading-only one — the ordering TestSearch_ChunkFieldBoosts pins at defaults.
func TestSearch_ReadsHeadingAndPathBoostsFromSettings(t *testing.T) {
	newService := func(t *testing.T, mutate func(*config.SearchSettings)) *Service {
		t.Helper()
		settings := testSettings()
		mutate(&settings)
		service := NewService(settings)
		t.Cleanup(service.Close)
		indexChunks(t, service, []domain.Chunk{
			{
				ID: "heading", SourceID: "heading-source",
				SourceURI: "acdc://heading", ChunkURI: "acdc://heading#chunk",
				HeadingPath: []string{"oauth"}, Content: "unrelated",
			},
			{
				ID: "body", SourceID: "body-source",
				SourceURI: "acdc://body", ChunkURI: "acdc://body#chunk",
				Content: "oauth",
			},
		})
		return service
	}

	t.Run("heading boost leads at the default", func(t *testing.T) {
		results, err := newService(t, func(*config.SearchSettings) {}).Search("oauth", 10)
		require.NoError(t, err)
		require.Len(t, results, 2)
		require.Equal(t, "heading", results[0].ChunkID)
	})

	t.Run("zero heading boost removes the clause", func(t *testing.T) {
		results, err := newService(t, func(s *config.SearchSettings) { s.HeadingBoost = 0 }).Search("oauth", 10)
		require.NoError(t, err)
		require.NotEmpty(t, results)
		require.Equal(t, "body", results[0].ChunkID)
	})

	t.Run("zero path boost removes the clause", func(t *testing.T) {
		service := NewService(func() config.SearchSettings {
			s := testSettings()
			s.PathBoost = 0
			s.ContentBoost = 0
			return s
		}())
		t.Cleanup(service.Close)
		indexChunks(t, service, []domain.Chunk{{
			ID: "path", SourceID: "path-source",
			SourceURI: "acdc://path", ChunkURI: "acdc://path#chunk",
			PathLabels: []string{"oauth"}, Content: "unrelated",
		}})

		results, err := service.Search("oauth", 10)
		require.NoError(t, err)
		require.Empty(t, results, "no clause can match once path and content boosts are zero")
	})
}

// rankedScore is a ranking projection: the identity of a hit and its score,
// without the snippet, whose highlight fragments are allowed to differ.
type rankedScore struct {
	URI   string
	Score float64
}

func rankedScores(results []SearchResult) []rankedScore {
	out := make([]rankedScore, 0, len(results))
	for _, result := range results {
		out = append(out, rankedScore{URI: result.ChunkURI, Score: result.Score})
	}
	return out
}

// searchThrough runs the production query shape against a caller-supplied
// index, bypassing Service.newIndex so a test can choose the index kind.
func searchThrough(t *testing.T, idx searchIndex, chunks []domain.Chunk, query string) []rankedScore {
	t.Helper()

	stream := make(chan domain.Chunk, len(chunks))
	for _, chunk := range chunks {
		stream <- chunk
	}
	close(stream)
	require.NoError(t, batchIndex(context.Background(), idx, stream))

	service := NewService(testSettings())
	service.index = idx
	t.Cleanup(service.Close)

	results, err := service.Search(query, 10)
	require.NoError(t, err)
	require.NotEmpty(t, results, "query %q retrieved nothing", query)
	return rankedScores(results)
}

// TestSearch_MemoryAndDiskIndexesHonourTheSameMapping pins the invariant that
// search.in_memory selects storage and never relevance: both index kinds must
// apply the same index mapping.
//
// The probe is BM25 scoring, which scorch implements and upsidedown does not.
// bleve does not report an error for a scoring model the index kind cannot
// support — it silently scores TF-IDF instead, because bm25ScoreMetrics leaves
// avgDocLength at 0 when the reader is not an index.BM25Reader and the scorer
// branches on avgDocLength > 0. ACDC does not ship BM25; it is used here only
// because it is a mapping feature whose absence is otherwise invisible.
//
// This asserts on Score, which the golden harness conventions forbid. That
// convention exists because absolute scores move with library upgrades and
// with corpus size. The assertion here is that two index kinds agree with each
// other, on one corpus and one library version, which is invariant to both.
func TestSearch_MemoryAndDiskIndexesHonourTheSameMapping(t *testing.T) {
	chunks := corpusChunks(t, zeroConfigCorpus)

	t.Run("a mapping feature only one index kind supports", func(t *testing.T) {
		bm25Mapping := func(t *testing.T) mapping.IndexMapping {
			t.Helper()
			m, ok := buildMapping().(*mapping.IndexMappingImpl)
			require.True(t, ok, "buildMapping must return *mapping.IndexMappingImpl")
			m.ScoringModel = bleveindex.BM25Scoring
			return m
		}

		memIndex, err := newMemoryIndex(bm25Mapping(t))
		require.NoError(t, err)
		diskIndex, err := newDiskIndex(filepath.Join(t.TempDir(), "idx"), bm25Mapping(t))
		require.NoError(t, err)

		require.Equal(t,
			searchThrough(t, diskIndex, chunks, "authentication"),
			searchThrough(t, memIndex, chunks, "authentication"),
			"the in-memory index ignored the mapping's scoring model")
	})

	t.Run("the shipped mapping", func(t *testing.T) {
		memIndex, err := newMemoryIndex(buildMapping())
		require.NoError(t, err)
		diskIndex, err := newDiskIndex(filepath.Join(t.TempDir(), "idx"), buildMapping())
		require.NoError(t, err)

		require.Equal(t,
			searchThrough(t, diskIndex, chunks, "authentication"),
			searchThrough(t, memIndex, chunks, "authentication"),
			"search.in_memory changed the ranking, which it must never do")
	})
}
