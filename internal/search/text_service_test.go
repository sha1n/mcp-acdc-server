package search

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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
	// Sizes of each submitted batch, in submission order.
	batchSizes []int
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
	m.batchSizes = append(m.batchSizes, batch.Size())
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

func indexChunks(t *testing.T, service *TextService, chunks []domain.Chunk) {
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
		require.ErrorContains(t, batchIndex(context.Background(), index, stream, defaultBatchSize), "failed to add chunk to batch")
	})
	t.Run("reports final batch failure", func(t *testing.T) {
		stream := make(chan domain.Chunk, 1)
		stream <- domain.Chunk{ID: "chunk", Content: "valid"}
		close(stream)
		mock := &mockBatchIndexer{realIndex: index, batchErr: errors.New("batch failed")}
		require.ErrorContains(t, batchIndex(context.Background(), mock, stream, defaultBatchSize), "failed to execute final batch index")
	})
	t.Run("reports full batch failure", func(t *testing.T) {
		// A batch size chosen independently of defaultBatchSize: this subtest
		// exercises the mid-stream flush (a batch reaching batchSize while more
		// chunks remain), not the constant's production value.
		const batchSize = 2
		stream := make(chan domain.Chunk, batchSize)
		for i := 0; i < batchSize; i++ {
			stream <- domain.Chunk{ID: fmt.Sprintf("chunk-%d", i), Content: "valid"}
		}
		close(stream)
		mock := &mockBatchIndexer{realIndex: index, batchErr: errors.New("batch failed")}
		require.ErrorContains(t, batchIndex(context.Background(), mock, stream, batchSize), "failed to execute batch index")
	})
}

func TestService_BatchIndexStartsFreshBatchAfterEachFlush(t *testing.T) {
	index, err := bleve.NewMemOnly(buildMapping())
	require.NoError(t, err)
	const batchSize = 2
	const chunkCount = 5

	stream := make(chan domain.Chunk, chunkCount)
	for i := 0; i < chunkCount; i++ {
		stream <- domain.Chunk{ID: fmt.Sprintf("chunk-%d", i), Content: "valid"}
	}
	close(stream)
	mock := &mockBatchIndexer{realIndex: index}

	require.NoError(t, batchIndex(context.Background(), mock, stream, batchSize))
	// A batch carried past its flush would grow to 2, 4, 5 instead.
	require.Equal(t, []int{2, 2, 1}, mock.batchSizes)
}

func TestService_IndexRebuildsAndBatchesChunks(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()
	// Indexed through TextService.Index, which takes no batch-size argument and
	// always batches at defaultBatchSize: the corpus must exceed that constant
	// for the rebuild below to flush more than one batch.
	chunks := make([]domain.Chunk, defaultBatchSize+50)
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
	newService := func(t *testing.T, mutate func(*config.SearchSettings)) *TextService {
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

// indexInto indexes chunks into idx at the given batch size and attaches the
// result to a TextService, bypassing TextService.newIndex so a test can choose the
// index kind and the batch size independently.
func indexInto(t *testing.T, idx searchIndex, chunks []domain.Chunk, batchSize int) *TextService {
	t.Helper()

	stream := make(chan domain.Chunk, len(chunks))
	for _, chunk := range chunks {
		stream <- chunk
	}
	close(stream)
	require.NoError(t, batchIndex(context.Background(), idx, stream, batchSize))

	service := NewService(testSettings())
	service.index = idx
	t.Cleanup(service.Close)
	return service
}

// searchThrough runs the production query shape against a caller-supplied
// index, bypassing TextService.newIndex so a test can choose the index kind.
func searchThrough(t *testing.T, idx searchIndex, chunks []domain.Chunk, query string) []rankedScore {
	t.Helper()

	service := indexInto(t, idx, chunks, defaultBatchSize)

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

// syntheticCorpusChunks builds n chunks whose content genuinely differs: each
// carries a topic word repeated a varying number of times (so term frequency,
// and therefore score, differs chunk to chunk), shared filler text, and a
// unique marker so no two chunks are identical.
func syntheticCorpusChunks(n int) []domain.Chunk {
	topics := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet"}
	const filler = "the system processes requests through a configured pipeline that handles each case differently depending on context and load"

	chunks := make([]domain.Chunk, n)
	for i := 0; i < n; i++ {
		topic := topics[i%len(topics)]
		reps := 1 + i%7
		content := strings.Repeat(topic+" ", reps) + filler + fmt.Sprintf(" marker%d", i)
		chunks[i] = domain.Chunk{
			ID:        fmt.Sprintf("chunk-%d", i),
			SourceID:  fmt.Sprintf("source-%d", i),
			SourceURI: fmt.Sprintf("acdc://source-%d", i),
			ChunkURI:  fmt.Sprintf("acdc://source-%d#chunk", i),
			Content:   content,
		}
	}
	return chunks
}

// batchIndexedService indexes chunks into a fresh in-memory index at the
// given batch size.
func batchIndexedService(t *testing.T, chunks []domain.Chunk, batchSize int) *TextService {
	t.Helper()

	idx, err := newMemoryIndex(buildMapping())
	require.NoError(t, err)
	return indexInto(t, idx, chunks, batchSize)
}

// serviceRootSegments counts the segments a query against service fans out
// across. No settling is needed: these services index in memory, where scorch
// runs neither the persister nor the merger, so the root snapshot is final the
// moment batchIndex returns.
func serviceRootSegments(t *testing.T, service *TextService) int {
	t.Helper()

	idx, ok := service.index.(bleve.Index)
	require.True(t, ok, "expected a bleve index")
	return int(rootSegments(t, idx))
}

// TestSearch_RankingIsIndependentOfBatchSize pins the invariant defaultBatchSize
// depends on: the batch size only bounds how many segments the in-memory index
// ends up with, and must never influence which documents match or how they
// score. The golden harness's corpus is far smaller than defaultBatchSize and
// so forms a single batch, which makes a golden-report diff across a
// batch-size change empty for a trivial reason — it never exercises more than
// one segment. This test builds a synthetic corpus of 300 chunks with
// varied content instead, so that the two sides genuinely differ in segment
// count, and checks that they agree anyway.
//
// The segment counts are asserted rather than assumed. The whole test rests on
// the two indexes being segmented differently, and a batch size that stopped
// producing that difference would leave the test comparing two identical
// indexes while still passing. The many-segment side is pinned at a literal
// 100 so it stays a fixed comparison point; the other side uses
// defaultBatchSize so this keeps covering the value actually shipped, which is
// why its expected segment count is stated as a relation rather than a number.
//
// This asserts on Score, which the golden harness's rank-only convention
// forbids elsewhere. That convention exists because absolute scores drift
// across library upgrades and across corpus sizes, so pinning a score also
// pins those incidental things. Here the two sides being compared share both
// the library version and the corpus — the only thing that differs is segment
// count — so a score difference could only come from segment count itself,
// which is exactly what this test exists to rule out. This is the same
// exception TestSearch_MemoryAndDiskIndexesHonourTheSameMapping takes, for
// the same reason: the invariant under test is agreement between two
// configurations of one index, not an absolute value.
func TestSearch_RankingIsIndependentOfBatchSize(t *testing.T) {
	const (
		corpusSize      = 300
		manySegmentSize = 100
	)
	chunks := syntheticCorpusChunks(corpusSize)

	manySegments := batchIndexedService(t, chunks, manySegmentSize)
	defaultBatched := batchIndexedService(t, chunks, defaultBatchSize)

	manyCount := serviceRootSegments(t, manySegments)
	defaultCount := serviceRootSegments(t, defaultBatched)
	require.Equal(t, corpusSize/manySegmentSize, manyCount,
		"batch size %d over %d chunks must yield one segment per batch", manySegmentSize, corpusSize)
	require.Less(t, defaultCount, manyCount,
		"batch size %d must segment the corpus differently from batch size %d, or this test compares two identically segmented indexes",
		defaultBatchSize, manySegmentSize)

	for _, query := range []string{"alpha", "delta", "hotel", "juliet", "marker137"} {
		t.Run(query, func(t *testing.T) {
			manyResults, err := manySegments.Search(query, 50)
			require.NoError(t, err)
			defaultResults, err := defaultBatched.Search(query, 50)
			require.NoError(t, err)
			require.NotEmpty(t, manyResults, "query %q retrieved nothing", query)

			require.Equal(t, rankedScores(defaultResults), rankedScores(manyResults),
				"batch size changed ranking or score for query %q", query)
		})
	}
}

// TestSearch_HighlightsMatchedContent covers the fragment Search returns as a
// result's Snippet. The highlight is requested with no field list, so bleve
// resolves it against the fields recorded in each hit's Locations — the
// fields that actually matched — rather than through the index mapping's
// default field, which is why the composite _all field being empty does not
// affect it.
func TestSearch_HighlightsMatchedContent(t *testing.T) {
	service := goldenService(t, zeroConfigCorpus)

	results, err := service.Search("bearer token", goldenCandidates)
	require.NoError(t, err)
	require.NotEmpty(t, results)
	require.Contains(t, results[0].Snippet, "<mark>",
		"the leading result must carry a highlighted fragment, not the raw chunk body")
}

// TestSearch_CompositeAllFieldIsEmpty pins that nothing is written to bleve's
// composite _all field. Every clause Search builds names an explicit field, so
// populating _all would index every token of five fields for no reader at all.
//
// Two probes are required because _all can be fed by two different analyzers,
// and each one only shows up under its own probe:
//
//   - "kubernet" is the "en" stem the mapped text fields' analyzer would write
//     into _all if IncludeInAll were true on those fields. _all itself is
//     always queried with the index mapping's default analyzer — "standard",
//     never "en" — so a query for the unstemmed word "kubernetes" never
//     reaches those tokens; only the stem does.
//   - "kubernetes" is the unstemmed word bleve's dynamic mapping would write
//     under the standard analyzer for any Chunk field left unmapped
//     (section_fragment, fragment, part, part_count) if dynamic mapping were
//     left on. The stem probe cannot catch that leak: dynamic mapping never
//     produces the "en" stem.
//
// Both probes must return zero hits, or the corresponding mapping setting has
// regressed.
//
// The field cannot be asserted away by name: IndexMappingImpl creates it
// unconditionally, so index.Fields() lists _all whether or not anything feeds
// it. Emptiness is only observable through a query.
func TestSearch_CompositeAllFieldIsEmpty(t *testing.T) {
	service := goldenService(t, zeroConfigCorpus)

	for _, probeTerm := range []string{"kubernet", "kubernetes"} {
		t.Run(probeTerm, func(t *testing.T) {
			probe := bleve.NewMatchQuery(probeTerm)
			probe.SetField("_all")
			request := bleve.NewSearchRequest(probe)
			request.Size = goldenCandidates

			result, err := service.index.Search(request)
			require.NoError(t, err)
			require.Empty(t, result.Hits, "no field may be composed into _all")
		})
	}

	t.Run("unmapped Chunk fields are not indexed", func(t *testing.T) {
		index, ok := service.index.(bleve.Index)
		require.True(t, ok, "service.index must be a bleve.Index to reach Fields()")

		fields, err := index.Fields()
		require.NoError(t, err)

		for _, unmapped := range []string{"section_fragment", "fragment", "part", "part_count"} {
			require.NotContains(t, fields, unmapped,
				"dynamic mapping must not index Chunk fields with no explicit mapping")
		}
	})
}

func TestTextService_FetchReturnsStoredMetadataForKnownIDs(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()

	indexChunks(t, service, []domain.Chunk{
		{
			ID: "c1", SourceID: "s1", SourceURI: "acdc://guides/a",
			ChunkURI: "acdc://guides/a#intro", SourceTitle: "Guide A",
			SourcePath: "guides/a.md", HeadingPath: []string{"Intro"},
			StartLine: 3, EndLine: 9, Content: "alpha content",
		},
		{
			ID: "c2", SourceID: "s2", SourceURI: "acdc://guides/b",
			ChunkURI: "acdc://guides/b#setup", SourceTitle: "Guide B",
			SourcePath: "guides/b.md", HeadingPath: []string{"Setup"},
			StartLine: 1, EndLine: 4, Content: "beta content",
		},
		{ID: "c3", SourceID: "s3", SourceURI: "acdc://guides/c", Content: "gamma"},
	})

	results, err := service.Fetch([]string{"c2", "c1"})
	require.NoError(t, err)
	require.Len(t, results, 2)

	byID := map[string]SearchResult{}
	for _, r := range results {
		byID[r.ChunkID] = r
	}

	require.Equal(t, "Guide A", byID["c1"].SourceTitle)
	require.Equal(t, "acdc://guides/a#intro", byID["c1"].ChunkURI)
	require.Equal(t, []string{"Intro"}, byID["c1"].HeadingPath)
	require.Equal(t, 3, byID["c1"].StartLine)
	require.Equal(t, 9, byID["c1"].EndLine)
	require.Equal(t, "alpha content", byID["c1"].Content)
	// No Highlight on a DocID lookup, so the snippet falls back to leading content.
	require.Equal(t, "alpha content", byID["c1"].Snippet)
	require.Equal(t, "Guide B", byID["c2"].SourceTitle)
}

func TestTextService_FetchToleratesUnknownAndEmptyIDs(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()

	indexChunks(t, service, []domain.Chunk{
		{ID: "c1", SourceID: "s1", SourceTitle: "Guide A", Content: "alpha"},
	})

	results, err := service.Fetch([]string{"c1", "missing"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "c1", results[0].ChunkID)

	empty, err := service.Fetch(nil)
	require.NoError(t, err)
	require.Empty(t, empty)
}

func TestTextService_FetchOnEmptyIndexReturnsNothing(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()

	results, err := service.Fetch([]string{"c1"})
	require.NoError(t, err)
	require.Empty(t, results)
}

// A DocID lookup has no query to highlight against, so the snippet is built
// from leading content. Without a bound the whole chunk becomes the snippet,
// which content mode then prints a second time as Content.
func TestTextService_FetchBoundsTheSnippetForLargeChunks(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()

	body := strings.Repeat("alpha ", 400)
	indexChunks(t, service, []domain.Chunk{
		{ID: "c1", SourceID: "s1", SourceURI: "acdc://guides/a", Content: body},
	})

	results, err := service.Fetch([]string{"c1"})
	require.NoError(t, err)
	require.Len(t, results, 1)

	require.Less(t, len([]rune(results[0].Snippet)), len([]rune(body)),
		"expected the snippet to be bounded, not the whole chunk")
	require.Equal(t, body, results[0].Content, "Content must stay whole")
}

func TestTextService_FetchKeepsShortContentIntactAsTheSnippet(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()

	indexChunks(t, service, []domain.Chunk{
		{ID: "c1", SourceID: "s1", SourceURI: "acdc://guides/a", Content: "alpha content"},
	})

	results, err := service.Fetch([]string{"c1"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "alpha content", results[0].Snippet)
}
