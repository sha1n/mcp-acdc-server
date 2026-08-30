package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/embed"
	"github.com/sha1n/mcp-acdc-server/internal/search/vector"
	"github.com/stretchr/testify/require"
)

// testSemanticSettings is testSettings() with semantic search configured. The
// model path doubles as the cache-key salt, so it only has to be stable.
func testSemanticSettings() config.SearchSettings {
	settings := testSettings()
	settings.SemanticModel = "test-model"
	settings.SemanticFloor = config.DefaultSemanticFloor
	return settings
}

func newTestHybrid(t *testing.T, embedder embed.Embedder) (*HybridService, *TextService) {
	t.Helper()
	lexical := NewService(testSettings())
	hybrid, err := NewHybridService(lexical, embedder, testSemanticSettings())
	require.NoError(t, err)
	t.Cleanup(hybrid.Close)
	return hybrid, lexical
}

func indexHybrid(t *testing.T, hybrid *HybridService, chunks []domain.Chunk) error {
	t.Helper()
	stream := make(chan domain.Chunk, len(chunks))
	for _, chunk := range chunks {
		stream <- chunk
	}
	close(stream)
	return hybrid.Index(context.Background(), stream)
}

func testChunks() []domain.Chunk {
	return []domain.Chunk{
		{ID: "c1", SourceID: "s1", SourceURI: "acdc://a", ChunkURI: "acdc://a#1", SourceTitle: "Alpha", Content: "alpha content"},
		{ID: "c2", SourceID: "s2", SourceURI: "acdc://b", ChunkURI: "acdc://b#1", SourceTitle: "Beta", Content: "beta content"},
	}
}

func TestHybridService_RejectsALexicalSideWithoutFetch(t *testing.T) {
	_, err := NewHybridService(&stubSearcher{}, embed.NewFake(8), testSemanticSettings())
	require.Error(t, err)
}

func TestHybridService_IndexFeedsBothSides(t *testing.T) {
	fake := embed.NewFake(8)
	hybrid, lexical := newTestHybrid(t, fake)

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))

	count, err := lexical.DocCount()
	require.NoError(t, err)
	require.Equal(t, uint64(2), count, "every chunk must reach the lexical index")
	require.Equal(t, 2, hybrid.VectorCount(), "each short chunk yields one passage")
	require.Equal(t, 2, fake.DocumentsEmbedded())
}

// D9's guard: an unchanged stream must not re-embed anything on the second
// pass. Asserted at Index rather than through Revalidate, because Revalidate
// short-circuits on the content digest before re-indexing unchanged content.
func TestHybridService_IndexCarriesUnchangedPassagesForward(t *testing.T) {
	fake := embed.NewFake(8)
	hybrid, _ := newTestHybrid(t, fake)

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))
	require.Equal(t, 2, fake.DocumentsEmbedded())

	fake.ResetCounts()
	require.NoError(t, indexHybrid(t, hybrid, testChunks()))

	require.Equal(t, 0, fake.DocumentsEmbedded(), "an identical stream must embed nothing")
	require.Equal(t, 2, hybrid.VectorCount())
}

func TestHybridService_IndexReEmbedsOnlyChangedPassages(t *testing.T) {
	fake := embed.NewFake(8)
	hybrid, _ := newTestHybrid(t, fake)

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))
	fake.ResetCounts()

	changed := testChunks()
	changed[1].Content = "beta content, revised"
	require.NoError(t, indexHybrid(t, hybrid, changed))

	require.Equal(t, 1, fake.DocumentsEmbedded(), "only the edited chunk re-embeds")
	require.Equal(t, 2, hybrid.VectorCount())
}

// The prefix is part of the embedded text, so a heading change invalidates the
// passage even when the body is byte-identical.
func TestHybridService_IndexReEmbedsWhenOnlyTheHeadingChanged(t *testing.T) {
	fake := embed.NewFake(8)
	hybrid, _ := newTestHybrid(t, fake)

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))
	fake.ResetCounts()

	reheaded := testChunks()
	reheaded[0].HeadingPath = []string{"New Section"}
	require.NoError(t, indexHybrid(t, hybrid, reheaded))

	require.Equal(t, 1, fake.DocumentsEmbedded())
}

// Generational retention: a grow-only cache would hold vectors for deleted
// documents forever.
func TestHybridService_IndexDropsVectorsForRemovedChunks(t *testing.T) {
	fake := embed.NewFake(8)
	hybrid, _ := newTestHybrid(t, fake)

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))
	require.NoError(t, indexHybrid(t, hybrid, testChunks()[:1]))

	require.Equal(t, 1, hybrid.VectorCount())

	// The dropped chunk is gone from the cache too, so re-adding it re-embeds.
	fake.ResetCounts()
	require.NoError(t, indexHybrid(t, hybrid, testChunks()))
	require.Equal(t, 1, fake.DocumentsEmbedded())
}

// Identical boilerplate across documents embeds once but is stored per chunk.
func TestHybridService_IndexDedupsIdenticalPassages(t *testing.T) {
	fake := embed.NewFake(8)
	hybrid, _ := newTestHybrid(t, fake)

	require.NoError(t, indexHybrid(t, hybrid, []domain.Chunk{
		{ID: "c1", SourceID: "s1", SourceTitle: "Shared", Content: "identical boilerplate"},
		{ID: "c2", SourceID: "s2", SourceTitle: "Shared", Content: "identical boilerplate"},
	}))

	require.Equal(t, 1, fake.DocumentsEmbedded(), "one embed for two identical passages")
	require.Equal(t, 2, hybrid.VectorCount(), "but a vector for each owning chunk")
}

// A vector index whose chunk IDs disagree with the lexical index is worse than
// no vector index at all, so a build failure serves lexical-only rather than
// failing startup that lexical retrieval could still serve.
func TestHybridService_IndexSurvivesAnEmbedderFailure(t *testing.T) {
	broken := &failingEmbedder{err: errors.New("model exploded")}
	hybrid, lexical := newTestHybrid(t, broken)

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))

	count, err := lexical.DocCount()
	require.NoError(t, err)
	require.Equal(t, uint64(2), count, "the lexical index stays complete")
	require.Equal(t, 0, hybrid.VectorCount(), "the mismatched vector index is cleared")
}

func TestHybridService_IndexClearsPreviousVectorsOnFailure(t *testing.T) {
	toggle := &failingEmbedder{Embedder: embed.NewFake(8)}
	hybrid, _ := newTestHybrid(t, toggle)

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))
	require.Equal(t, 2, hybrid.VectorCount())

	toggle.err = errors.New("model exploded")
	// The content must differ from the first pass. A fully cached generation
	// never reaches the embedder, so the failure path is only reachable when at
	// least one passage misses the carry-forward cache.
	changed := testChunks()
	changed[1].Content = "beta content, revised"
	require.NoError(t, indexHybrid(t, hybrid, changed))

	require.Equal(t, 0, hybrid.VectorCount(), "stale vectors must not outlive a failed rebuild")
}

func TestHybridService_IndexPropagatesLexicalFailure(t *testing.T) {
	lexical := &stubRetriever{indexErr: errors.New("bleve exploded")}
	hybrid, err := NewHybridService(lexical, embed.NewFake(8), testSemanticSettings())
	require.NoError(t, err)

	require.Error(t, indexHybrid(t, hybrid, testChunks()))
}

// TextService.Index keeps its previous index when a rebuild fails, and the
// catalog keeps its previous generation. The retained bleve index and the
// retained vector generation are the same generation, so they must survive or
// fall together: the failed stream's vectors are never published, and the
// surviving ones are never cleared.
func TestHybridService_IndexKeepsThePreviousGenerationWhenTheLexicalSideFails(t *testing.T) {
	fake := embed.NewFake(8)
	lexical := &stubRetriever{}
	hybrid, err := NewHybridService(lexical, fake, testSemanticSettings())
	require.NoError(t, err)
	t.Cleanup(hybrid.Close)

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))
	require.Equal(t, 2, hybrid.VectorCount())

	lexical.indexErr = errors.New("bleve exploded")
	require.Error(t, indexHybrid(t, hybrid, testChunks()[:1]))

	// One count proves both halves: 1 would mean the failed generation was
	// published, 0 would mean the surviving generation was cleared.
	require.Equal(t, 2, hybrid.VectorCount(), "the generation still serving lexically must keep its vectors")

	// The carry-forward cache survived with it, so recovery costs no inference.
	lexical.indexErr = nil
	fake.ResetCounts()
	require.NoError(t, indexHybrid(t, hybrid, testChunks()))

	require.Equal(t, 0, fake.DocumentsEmbedded(), "a failed rebuild must not discard the carry-forward cache")
	require.Equal(t, 2, hybrid.VectorCount())
}

// Carry-forward has to reach the passages halving produced, not only the ones
// BuildPassages emitted: those are the passages the previous generation paid
// the most for.
func TestHybridService_IndexCarriesHalvedPassagesForward(t *testing.T) {
	fake := embed.NewFake(8)
	fake.MaxTokens = 40
	hybrid, _ := newTestHybrid(t, fake)

	oversized := []domain.Chunk{
		{ID: "c1", SourceID: "s1", SourceTitle: "T", Content: strings.Repeat("x", 100)},
	}

	require.NoError(t, indexHybrid(t, hybrid, oversized))
	first := hybrid.VectorCount()
	require.Greater(t, first, 1)

	fake.ResetCounts()
	require.NoError(t, indexHybrid(t, hybrid, oversized))

	require.Equal(t, 0, fake.DocumentsEmbedded(), "halved passages a prior generation embedded must come from cache")
	require.Equal(t, first, hybrid.VectorCount())
}

// The tee must never block the upstream producer, whichever side stops first.
func TestHybridService_IndexDoesNotDeadlockWhenLexicalStopsReading(t *testing.T) {
	lexical := &stubRetriever{indexErr: errors.New("bleve exploded"), stopReading: true}
	hybrid, err := NewHybridService(lexical, embed.NewFake(8), testSemanticSettings())
	require.NoError(t, err)

	chunks := make([]domain.Chunk, 500)
	for i := range chunks {
		chunks[i] = domain.Chunk{ID: string(rune('a'+i%26)) + string(rune('a'+i/26)), SourceTitle: "T", Content: "body"}
	}

	done := make(chan error, 1)
	go func() { done <- indexHybrid(t, hybrid, chunks) }()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Index deadlocked when the lexical side stopped reading")
	}
}

// The rune-per-token estimate is a starting point, not a guarantee. A model
// that rejects an over-window passage must see it halved and retried, not
// dropped.
func TestHybridService_IndexHalvesAndRetriesOversizedPassages(t *testing.T) {
	fake := embed.NewFake(8)
	fake.MaxTokens = 40
	hybrid, _ := newTestHybrid(t, fake)

	require.NoError(t, indexHybrid(t, hybrid, []domain.Chunk{
		{ID: "c1", SourceID: "s1", SourceTitle: "T", Content: strings.Repeat("x", 100)},
	}))

	require.Greater(t, hybrid.VectorCount(), 1, "the oversized passage split rather than vanished")
	require.Greater(t, fake.DocumentsEmbedded(), 1)
}

// Halving bottoms out at a single indivisible unit; that passage is skipped
// and counted, and its chunk stays lexically findable.
func TestHybridService_IndexSkipsPassagesHalvingCannotRescue(t *testing.T) {
	fake := embed.NewFake(8)
	fake.MaxTokens = 1
	hybrid, lexical := newTestHybrid(t, fake)

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))

	require.Equal(t, 0, hybrid.VectorCount())
	count, err := lexical.DocCount()
	require.NoError(t, err)
	require.Equal(t, uint64(2), count, "a skipped chunk remains lexically findable")
}

func TestHybridService_CloseReleasesTheLexicalSideButNotTheEmbedder(t *testing.T) {
	closing := &closableEmbedder{Embedder: embed.NewFake(8)}
	lexical := NewService(testSettings())
	hybrid, err := NewHybridService(lexical, closing, testSemanticSettings())
	require.NoError(t, err)

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))
	hybrid.Close()

	require.Equal(t, 0, hybrid.VectorCount())
	require.False(t, closing.closed, "the factory owns the embedder's lifetime, not the service")

	count, err := lexical.DocCount()
	require.NoError(t, err)
	require.Equal(t, uint64(0), count)
}

// --- fakes ---

// stubSearcher satisfies Searcher but not textRetriever.
type stubSearcher struct{}

func (s *stubSearcher) Search(string, int) ([]SearchResult, error) { return nil, nil }
func (s *stubSearcher) Index(context.Context, <-chan domain.Chunk) error {
	return nil
}
func (s *stubSearcher) Close() {}

type stubRetriever struct {
	indexErr    error
	stopReading bool
	results     []SearchResult
	searchErr   error
	fetched     []SearchResult
	fetchErr    error
	built       []domain.Chunk
	indexed     []domain.Chunk
	published   int
}

func (s *stubRetriever) Index(ctx context.Context, chunks <-chan domain.Chunk) error {
	generation, err := s.build(ctx, chunks)
	if err != nil {
		return err
	}
	s.publish(generation)

	return nil
}

func (s *stubRetriever) build(_ context.Context, chunks <-chan domain.Chunk) (*lexicalGeneration, error) {
	if s.stopReading {
		return nil, s.indexErr
	}
	for chunk := range chunks {
		s.built = append(s.built, chunk)
	}
	if s.indexErr != nil {
		return nil, s.indexErr
	}

	return &lexicalGeneration{}, nil
}

func (s *stubRetriever) publish(*lexicalGeneration) {
	s.indexed = s.built
	s.published++
}

func (s *stubRetriever) Search(string, int) ([]SearchResult, error) { return s.results, s.searchErr }
func (s *stubRetriever) Fetch([]string) ([]SearchResult, error)     { return s.fetched, s.fetchErr }
func (s *stubRetriever) Close()                                     {}

type failingEmbedder struct {
	embed.Embedder
	err error
}

func (f *failingEmbedder) Info() embed.ModelInfo {
	if f.Embedder != nil {
		return f.Embedder.Info()
	}
	return embed.ModelInfo{Dimensions: 8}
}

func (f *failingEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.Embedder.EmbedDocuments(ctx, texts)
}

func (f *failingEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.Embedder.EmbedQuery(ctx, text)
}

type closableEmbedder struct {
	embed.Embedder
	closed bool
}

func (c *closableEmbedder) Close() error {
	c.closed = true
	return nil
}

// scriptedEmbedder maps a substring of the embedded text to an explicit
// vector, so fused order is deterministic and reviewable by eye. Hash-derived
// fakes are near-orthogonal by construction and say nothing about ranking.
type scriptedEmbedder struct {
	dimensions int
	vectors    map[string][]float32
	queryErr   error
}

func (s *scriptedEmbedder) Info() embed.ModelInfo {
	return embed.ModelInfo{Dimensions: s.dimensions}
}

func (s *scriptedEmbedder) lookup(text string) []float32 {
	for marker, vector := range s.vectors {
		if strings.Contains(text, marker) {
			return append([]float32(nil), vector...)
		}
	}
	// Orthogonal to every scripted vector, so an unscripted text matches nothing.
	orthogonal := make([]float32, s.dimensions)
	orthogonal[s.dimensions-1] = 1
	return orthogonal
}

func (s *scriptedEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		vectors[i] = s.lookup(text)
	}
	return vectors, nil
}

func (s *scriptedEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	if s.queryErr != nil {
		return nil, s.queryErr
	}
	return s.lookup(text), nil
}

func fusionEmbedder() *scriptedEmbedder {
	return &scriptedEmbedder{
		dimensions: 3,
		vectors: map[string][]float32{
			"alpha": {1, 0, 0},
			"gamma": {0.7071068, 0.7071068, 0},
			"beta":  {0, 1, 0},
		},
	}
}

func fusionChunks() []domain.Chunk {
	return []domain.Chunk{
		{ID: "c1", SourceID: "s1", SourceURI: "acdc://a", ChunkURI: "acdc://a#1", SourceTitle: "A", Content: "alpha body"},
		{ID: "c2", SourceID: "s2", SourceURI: "acdc://b", ChunkURI: "acdc://b#1", SourceTitle: "B", Content: "beta body"},
		{ID: "c3", SourceID: "s3", SourceURI: "acdc://c", ChunkURI: "acdc://c#1", SourceTitle: "C", Content: "gamma body"},
	}
}

func newFusionHybrid(t *testing.T, embedder embed.Embedder) (*HybridService, *stubRetriever) {
	t.Helper()
	lexical := &stubRetriever{}
	hybrid, err := NewHybridService(lexical, embedder, testSemanticSettings())
	require.NoError(t, err)
	require.NoError(t, indexHybrid(t, hybrid, fusionChunks()))
	return hybrid, lexical
}

func resultIDs(results []SearchResult) []string {
	ids := make([]string, len(results))
	for i, result := range results {
		ids[i] = result.ChunkID
	}
	return ids
}

// A chunk both retrievers found outranks one either found alone. Lexical order
// is c2, c1, c3; the query vector puts c1 first and c3 second.
func TestHybridService_SearchFusesBothRankings(t *testing.T) {
	hybrid, lexical := newFusionHybrid(t, fusionEmbedder())
	lexical.results = []SearchResult{
		{ChunkID: "c2", SourceURI: "acdc://b", SourceTitle: "B", Score: 4.2},
		{ChunkID: "c1", SourceURI: "acdc://a", SourceTitle: "A", Score: 2.1},
		{ChunkID: "c3", SourceURI: "acdc://c", SourceTitle: "C", Score: 1.0},
	}

	results, err := hybrid.Search("alpha", 10)

	require.NoError(t, err)
	require.Equal(t, []string{"c1", "c3", "c2"}, resultIDs(results))
}

// RRF scores cluster near 1/60, so search_presenter's "score %.2f" would print
// every result identically. Normalizing keeps that format intact for the
// lexical-only path, which must stay byte-identical.
func TestHybridService_SearchNormalizesFusedScoresForTwoDecimalRendering(t *testing.T) {
	hybrid, lexical := newFusionHybrid(t, fusionEmbedder())
	lexical.results = []SearchResult{
		{ChunkID: "c2", SourceURI: "acdc://b"},
		{ChunkID: "c1", SourceURI: "acdc://a"},
		{ChunkID: "c3", SourceURI: "acdc://c"},
	}

	results, err := hybrid.Search("alpha", 10)

	require.NoError(t, err)
	require.Len(t, results, 3)
	require.InDelta(t, 1.0, results[0].Score, 1e-9, "the top fused result anchors the scale")

	rendered := map[string]bool{}
	for _, result := range results {
		rendered[fmt.Sprintf("%.2f", result.Score)] = true
	}
	require.Len(t, rendered, 3, "fused scores must stay distinguishable at two decimals")
}

// A chunk only the vector side found has no metadata of its own: the vector
// store holds IDs and scores, never a second copy of chunk content.
func TestHybridService_SearchHydratesSemanticOnlyHitsThroughFetch(t *testing.T) {
	hybrid, lexical := newFusionHybrid(t, fusionEmbedder())
	lexical.results = []SearchResult{{ChunkID: "c2", SourceURI: "acdc://b", SourceTitle: "B"}}
	lexical.fetched = []SearchResult{
		{ChunkID: "c1", SourceID: "s1", SourceURI: "acdc://a", ChunkURI: "acdc://a#1", SourceTitle: "A", Content: "alpha body", Snippet: "alpha body"},
		{ChunkID: "c3", SourceID: "s3", SourceURI: "acdc://c", ChunkURI: "acdc://c#1", SourceTitle: "C", Content: "gamma body", Snippet: "gamma body"},
	}

	results, err := hybrid.Search("alpha", 10)

	require.NoError(t, err)
	require.Equal(t, "c1", results[0].ChunkID)
	require.Equal(t, "A", results[0].SourceTitle)
	require.Equal(t, "acdc://a#1", results[0].ChunkURI)
	require.Equal(t, "alpha body", results[0].Snippet)
}

func TestHybridService_SearchPropagatesFetchFailure(t *testing.T) {
	hybrid, lexical := newFusionHybrid(t, fusionEmbedder())
	lexical.results = []SearchResult{{ChunkID: "c2", SourceURI: "acdc://b"}}
	lexical.fetchErr = errors.New("bleve exploded")

	_, err := hybrid.Search("alpha", 10)

	require.Error(t, err)
}

// Without a floor, a query with no real match would return the corpus's
// nearest-but-irrelevant chunks where today it returns "No results found".
func TestHybridService_SearchReturnsNothingWhenNothingClearsTheFloor(t *testing.T) {
	hybrid, lexical := newFusionHybrid(t, fusionEmbedder())
	lexical.results = nil

	results, err := hybrid.Search("wholly unrelated nonsense", 10)

	require.NoError(t, err)
	require.Empty(t, results)
}

func TestHybridService_SearchDropsHitsBelowTheFloor(t *testing.T) {
	hybrid, lexical := newFusionHybrid(t, fusionEmbedder())
	lexical.results = nil
	lexical.fetched = []SearchResult{
		{ChunkID: "c1", SourceURI: "acdc://a", SourceTitle: "A"},
		{ChunkID: "c2", SourceURI: "acdc://b", SourceTitle: "B"},
		{ChunkID: "c3", SourceURI: "acdc://c", SourceTitle: "C"},
	}

	results, err := hybrid.Search("alpha", 10)

	require.NoError(t, err)
	// c1 at 1.0 and c3 at 0.707 clear the default 0.25 floor; c2 at 0.0 does
	// not, and Fetch offering its metadata does not put it back.
	require.Equal(t, []string{"c1", "c3"}, resultIDs(results))
}

// The floor comes from settings, not from a constant. These two pin both ends
// of the range an operator can reach.
func TestHybridService_SearchHonoursAConfiguredFloor(t *testing.T) {
	settings := testSemanticSettings()
	settings.SemanticFloor = 0.9

	lexical := &stubRetriever{fetched: []SearchResult{
		{ChunkID: "c1", SourceURI: "acdc://a"},
		{ChunkID: "c3", SourceURI: "acdc://c"},
	}}
	hybrid, err := NewHybridService(lexical, fusionEmbedder(), settings)
	require.NoError(t, err)
	require.NoError(t, indexHybrid(t, hybrid, fusionChunks()))

	results, err := hybrid.Search("alpha", 10)

	require.NoError(t, err)
	require.Equal(t, []string{"c1"}, resultIDs(results), "c3 at 0.707 no longer clears a floor of 0.9")
}

func TestHybridService_SearchWithFloorDisabledKeepsEveryHit(t *testing.T) {
	settings := testSemanticSettings()
	settings.SemanticFloor = -1

	lexical := &stubRetriever{fetched: []SearchResult{
		{ChunkID: "c1", SourceURI: "acdc://a"},
		{ChunkID: "c2", SourceURI: "acdc://b"},
		{ChunkID: "c3", SourceURI: "acdc://c"},
	}}
	hybrid, err := NewHybridService(lexical, fusionEmbedder(), settings)
	require.NoError(t, err)
	require.NoError(t, indexHybrid(t, hybrid, fusionChunks()))

	results, err := hybrid.Search("alpha", 10)

	require.NoError(t, err)
	require.Equal(t, []string{"c1", "c3", "c2"}, resultIDs(results),
		"-1 disables the floor, so even a cosine of 0 survives")
}

func TestHybridService_SearchSkipsSemanticForMatchAll(t *testing.T) {
	hybrid, lexical := newFusionHybrid(t, fusionEmbedder())
	lexical.results = []SearchResult{{ChunkID: "c2", SourceURI: "acdc://b", Score: 1.0}}

	results, err := hybrid.Search("*", 10)

	require.NoError(t, err)
	require.Equal(t, []string{"c2"}, resultIDs(results))
	require.Equal(t, 1.0, results[0].Score, "match-all returns the lexical results untouched")
}

// A query-time embedding failure degrades to lexical retrieval rather than
// failing the request; only index-time model loading is fatal, and that is the
// factory's business.
func TestHybridService_SearchDegradesToLexicalWhenQueryEmbeddingFails(t *testing.T) {
	embedder := fusionEmbedder()
	hybrid, lexical := newFusionHybrid(t, embedder)
	lexical.results = []SearchResult{{ChunkID: "c2", SourceURI: "acdc://b", Score: 3.3}}
	embedder.queryErr = errors.New("inference exploded")

	results, err := hybrid.Search("alpha", 10)

	require.NoError(t, err)
	require.Equal(t, []string{"c2"}, resultIDs(results))
	require.Equal(t, 3.3, results[0].Score)
}

func TestHybridService_SearchPropagatesLexicalFailure(t *testing.T) {
	hybrid, lexical := newFusionHybrid(t, fusionEmbedder())
	lexical.searchErr = errors.New("bleve exploded")

	_, err := hybrid.Search("alpha", 10)

	require.Error(t, err)
}

func TestHybridService_SearchWithoutVectorsMatchesLexicalExactly(t *testing.T) {
	lexical := &stubRetriever{results: []SearchResult{{ChunkID: "c2", SourceURI: "acdc://b", Score: 7.5}}}
	hybrid, err := NewHybridService(lexical, fusionEmbedder(), testSemanticSettings())
	require.NoError(t, err)

	results, err := hybrid.Search("alpha", 10)

	require.NoError(t, err)
	require.Equal(t, lexical.results, results, "an unbuilt vector index changes nothing")
}

func TestHybridService_SearchTruncatesToCandidateLimit(t *testing.T) {
	hybrid, lexical := newFusionHybrid(t, fusionEmbedder())
	lexical.results = []SearchResult{
		{ChunkID: "c2", SourceURI: "acdc://b"},
		{ChunkID: "c1", SourceURI: "acdc://a"},
		{ChunkID: "c3", SourceURI: "acdc://c"},
	}

	results, err := hybrid.Search("alpha", 2)

	require.NoError(t, err)
	require.Len(t, results, 2, "a returned count below the window still means candidates are exhausted")
	require.Equal(t, []string{"c1", "c3"}, resultIDs(results))
}

// A vector for a chunk the lexical index no longer holds must be dropped, not
// returned without metadata.
func TestHybridService_SearchDropsHitsWithNoLexicalDocument(t *testing.T) {
	hybrid, lexical := newFusionHybrid(t, fusionEmbedder())
	lexical.results = nil
	lexical.fetched = nil

	results, err := hybrid.Search("alpha", 10)

	require.NoError(t, err)
	require.Empty(t, results)
}

func TestHybridService_SearchBreaksTiesDeterministically(t *testing.T) {
	hybrid, lexical := newFusionHybrid(t, fusionEmbedder())
	lexical.results = []SearchResult{
		{ChunkID: "c3", SourceURI: "acdc://c"},
		{ChunkID: "c2", SourceURI: "acdc://b"},
	}

	first, err := hybrid.Search("beta", 10)
	require.NoError(t, err)
	for i := 0; i < 20; i++ {
		again, err := hybrid.Search("beta", 10)
		require.NoError(t, err)
		require.Equal(t, resultIDs(first), resultIDs(again))
	}
}

// A hybrid retriever is a Searcher like any other: a non-positive candidate
// limit must fall back to settings.MaxResults exactly as TextService.Search
// does, rather than scanning and returning the whole corpus.
func TestHybridService_SearchFallsBackToMaxResultsForNonPositiveLimits(t *testing.T) {
	chunks := make([]domain.Chunk, 12)
	lexicalOrder := make([]SearchResult, len(chunks))
	for i := range chunks {
		id := fmt.Sprintf("c%02d", i+1)
		uri := "acdc://a" + id
		chunks[i] = domain.Chunk{
			ID: id, SourceID: id, SourceURI: uri, ChunkURI: uri + "#1",
			SourceTitle: id, Content: "alpha body " + id,
		}
		// Reversed, so a result led by c01 can only come from the vector side.
		lexicalOrder[len(chunks)-1-i] = SearchResult{ChunkID: id, SourceURI: uri, SourceTitle: id}
	}

	lexical := &stubRetriever{results: lexicalOrder}
	settings := testSemanticSettings()
	require.Equal(t, 10, settings.MaxResults, "the fallback under test")
	hybrid, err := NewHybridService(lexical, fusionEmbedder(), settings)
	require.NoError(t, err)
	require.NoError(t, indexHybrid(t, hybrid, chunks))
	require.Equal(t, 12, hybrid.VectorCount(), "every chunk must reach the vector side")

	for _, candidateLimit := range []int{0, -1} {
		t.Run(fmt.Sprintf("limit %d", candidateLimit), func(t *testing.T) {
			results, err := hybrid.Search("alpha", candidateLimit)

			require.NoError(t, err)
			require.Len(t, results, settings.MaxResults,
				"a non-positive limit must cap at MaxResults, not at the corpus size")
			require.Equal(t, "c01", results[0].ChunkID,
				"the lexical ranking put c01 last, so a fused result proves the vector side contributed")
		})
	}
}

// A non-positive MaxResults survives effectiveLimit, so the cap has to hold one
// level down too. TextService.Search hands bleve a Size of 0 and returns
// nothing; the semantic side must agree rather than contributing the corpus.
func TestHybridService_SearchContributesNothingWhenTheConfiguredLimitIsNonPositive(t *testing.T) {
	chunks := make([]domain.Chunk, 12)
	for i := range chunks {
		id := fmt.Sprintf("c%02d", i+1)
		uri := "acdc://a" + id
		chunks[i] = domain.Chunk{
			ID: id, SourceID: id, SourceURI: uri, ChunkURI: uri + "#1",
			SourceTitle: id, Content: "alpha body " + id,
		}
	}

	settings := testSemanticSettings()
	settings.MaxResults = 0

	lexical := &stubRetriever{
		results: []SearchResult{{ChunkID: "c05", SourceURI: "acdc://ac05", SourceTitle: "c05", Score: 6.5}},
		// Hydration must never be reached; offering metadata for the whole
		// corpus makes a leaking semantic side visible rather than silent.
		fetched: fetchableResults(chunks),
	}
	hybrid, err := NewHybridService(lexical, fusionEmbedder(), settings)
	require.NoError(t, err)
	require.NoError(t, indexHybrid(t, hybrid, chunks))
	require.Equal(t, 12, hybrid.VectorCount(), "the vectors exist; the limit is what must suppress them")

	results, err := hybrid.Search("alpha", 0)

	require.NoError(t, err)
	require.Len(t, results, 1, "a non-positive configured limit must not let the corpus through")
	require.Equal(t, lexical.results, results,
		"the semantic side must not answer a question the lexical side declined")
}

func fetchableResults(chunks []domain.Chunk) []SearchResult {
	results := make([]SearchResult, len(chunks))
	for i, chunk := range chunks {
		results[i] = SearchResult{
			ChunkID:   chunk.ID,
			SourceURI: chunk.SourceURI,
			ChunkURI:  chunk.ChunkURI,
			// SourceTitle is deliberately the chunk's own, so a leaked result is
			// identifiable in a failure message.
			SourceTitle: chunk.SourceTitle,
		}
	}
	return results
}

// --- multi-batch indexing ---

// multiBatchCorpus is large enough to flush embedBatchSize at least three
// times, so the state buildVectors carries between batches is exercised
// rather than assumed.
const multiBatchCorpus = 150

// multiBatchChunks builds a stream of distinct single-passage chunks.
func multiBatchChunks(count int) []domain.Chunk {
	chunks := make([]domain.Chunk, count)
	for i := range chunks {
		id := fmt.Sprintf("c%03d", i)
		uri := "acdc://" + id
		chunks[i] = domain.Chunk{
			ID: id, SourceID: id, SourceURI: uri, ChunkURI: uri + "#1",
			SourceTitle: "T", Content: "body " + id,
		}
	}
	return chunks
}

// The carry-forward cache is generation-wide, but the batch loop is not: next
// is shared across every flush while queued is rebuilt per batch. Every real
// corpus flushes mid-stream, so the cross-batch behaviour is the common case,
// not an edge.
func TestHybridService_IndexCarriesPassagesForwardAcrossBatches(t *testing.T) {
	require.Greater(t, multiBatchCorpus, 2*embedBatchSize, "the corpus must span at least three batches")

	fake := embed.NewFake(8)
	hybrid, _ := newTestHybrid(t, fake)
	chunks := multiBatchChunks(multiBatchCorpus)

	require.NoError(t, indexHybrid(t, hybrid, chunks))
	require.Equal(t, multiBatchCorpus, hybrid.VectorCount(), "each short chunk yields one passage")
	require.Equal(t, multiBatchCorpus, fake.DocumentsEmbedded())

	fake.ResetCounts()
	require.NoError(t, indexHybrid(t, hybrid, chunks))
	require.Equal(t, 0, fake.DocumentsEmbedded(), "an identical multi-batch stream must embed nothing")
	require.Equal(t, multiBatchCorpus, hybrid.VectorCount())

	fake.ResetCounts()
	changed := multiBatchChunks(multiBatchCorpus)
	// Deliberately in the second batch: a per-batch cache would re-embed every
	// passage that followed the first flush.
	changed[70].Content = "body c070, revised"
	require.NoError(t, indexHybrid(t, hybrid, changed))

	require.Equal(t, 1, fake.DocumentsEmbedded(), "only the edited chunk re-embeds, wherever its batch falls")
	require.Equal(t, multiBatchCorpus, hybrid.VectorCount())
}

// Dedup within one batch is queued's job; dedup across batches is only
// possible through the generation-wide next map.
func TestHybridService_IndexDedupsIdenticalPassagesAcrossBatches(t *testing.T) {
	fake := embed.NewFake(8)
	hybrid, _ := newTestHybrid(t, fake)

	chunks := multiBatchChunks(multiBatchCorpus)
	twin := chunks[0]
	chunks[100].SourceTitle = twin.SourceTitle
	chunks[100].HeadingPath = twin.HeadingPath
	chunks[100].Content = twin.Content

	require.NoError(t, indexHybrid(t, hybrid, chunks))

	require.Equal(t, multiBatchCorpus-1, fake.DocumentsEmbedded(),
		"one inference for a pair straddling a batch boundary")
	require.Equal(t, multiBatchCorpus, hybrid.VectorCount(), "but a vector for each owning chunk")
}

// --- malformed embedder responses ---

// Index swallows vector-build failures so the lexical side still publishes,
// so the contract these pin is the published state, not the returned error: a
// generation whose vectors do not line up with its chunks must be rejected
// whole rather than published half-aligned.
func TestHybridService_IndexRejectsAMalformedEmbedderResponse(t *testing.T) {
	tests := []struct {
		name     string
		embedder embed.Embedder
	}{
		{"fewer vectors than texts", &malformedEmbedder{dimensions: 8, vectorWidth: 8, dropOne: true}},
		{"vectors narrower than the declared dimensions", &malformedEmbedder{dimensions: 8, vectorWidth: 4}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hybrid, lexical := newTestHybrid(t, tc.embedder)

			require.NoError(t, indexHybrid(t, hybrid, testChunks()), "lexical retrieval must survive")

			require.Equal(t, 0, hybrid.VectorCount(), "a half-aligned generation must not publish")
			count, err := lexical.DocCount()
			require.NoError(t, err)
			require.Equal(t, uint64(2), count, "the lexical index stays complete")
		})
	}
}

// A model swapped in place behind a stable configured path changes the vector
// width while the path stays put, so the width is what has to invalidate the
// carry-forward cache. Nothing carries, and the generation publishes at the
// new width rather than degrading to lexical-only.
func TestHybridService_IndexRekeysWhenTheVectorWidthChanges(t *testing.T) {
	swappable := &mutableEmbedder{Embedder: embed.NewFake(8)}
	hybrid, lexical := newTestHybrid(t, swappable)

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))
	require.Equal(t, 2, hybrid.VectorCount())

	swapped := embed.NewFake(4)
	swappable.Embedder = swapped
	require.NoError(t, indexHybrid(t, hybrid, testChunks()))

	require.Positive(t, swapped.DocumentsEmbedded(), "vectors of the previous width must not carry forward")
	require.Equal(t, 2, hybrid.VectorCount(), "the generation publishes at the new width")
	count, err := lexical.DocCount()
	require.NoError(t, err)
	require.Equal(t, uint64(2), count, "the lexical index stays complete")
}

// An adapter that reports a too-long index outside the batch it was handed
// has violated its contract; there is no passage to halve or skip. The whole
// vector generation fails rather than panicking on the slice or guessing.
func TestHybridService_IndexRejectsAnOutOfRangeTooLongIndex(t *testing.T) {
	for _, reported := range []int{99, -1} {
		t.Run(fmt.Sprintf("reported index %d", reported), func(t *testing.T) {
			broken := &failingEmbedder{err: &embed.ErrInputTooLong{Index: reported}}
			hybrid, lexical := newTestHybrid(t, broken)

			// One chunk, so the batch holds exactly one text and every
			// reported index but 0 is out of range.
			require.NoError(t, indexHybrid(t, hybrid, testChunks()[:1]))

			require.Equal(t, 0, hybrid.VectorCount())
			count, err := lexical.DocCount()
			require.NoError(t, err)
			require.Equal(t, uint64(1), count, "the lexical index stays complete")
		})
	}
}

// --- concurrency ---

// Index publishes a new generation under the same lock Search reads it
// through. The assertion is that no Search fails and the last generation is
// the one serving; the value of the test is what -race sees while it runs.
func TestHybridService_SearchIsSafeWhileIndexRebuilds(t *testing.T) {
	const searchers = 8
	const maxSearches = 200
	finalCorpus := 20

	hybrid, _ := newTestHybrid(t, embed.NewFake(8))
	require.NoError(t, indexHybrid(t, hybrid, multiBatchChunks(30)))

	indexing := make(chan struct{})
	errs := make([]error, searchers)
	var searching sync.WaitGroup

	for slot := 0; slot < searchers; slot++ {
		searching.Add(1)
		go func(slot int) {
			defer searching.Done()
			for n := 0; n < maxSearches; n++ {
				select {
				case <-indexing:
					return
				default:
				}
				if _, err := hybrid.Search("body", 5); err != nil {
					errs[slot] = err
					return
				}
			}
		}(slot)
	}

	for _, size := range []int{45, 30, finalCorpus} {
		require.NoError(t, indexHybrid(t, hybrid, multiBatchChunks(size)))
	}
	close(indexing)
	searching.Wait()

	for slot, err := range errs {
		require.NoError(t, err, "searcher %d failed during a live rebuild", slot)
	}
	require.Equal(t, finalCorpus, hybrid.VectorCount(), "the last generation is the one left serving")
}

// --- fused tie-break ---

// The ChunkID rung of the fused sort only fires for two chunks of the same
// document, which is why a repeatability assertion alone is not enough: a
// map-order-dependent implementation satisfies repeatability by accident on a
// two-element slice but not an exact order.
func TestHybridService_SearchBreaksScoreTiesOnChunkIDWithinOneDocument(t *testing.T) {
	chunks := []domain.Chunk{
		{ID: "c1", SourceID: "s1", SourceURI: "acdc://same", ChunkURI: "acdc://same#1", SourceTitle: "S", Content: "alpha body"},
		{ID: "c2", SourceID: "s1", SourceURI: "acdc://same", ChunkURI: "acdc://same#2", SourceTitle: "S", Content: "gamma body"},
	}
	// The lexical ranking is the exact reverse of the vector one, so both
	// chunks accumulate the same pair of reciprocals and only the tie-break
	// decides. Lexical order is descending by ChunkID, so an ascending result
	// can only come from the ChunkID rung.
	lexical := &stubRetriever{results: []SearchResult{
		{ChunkID: "c2", SourceURI: "acdc://same", SourceTitle: "S"},
		{ChunkID: "c1", SourceURI: "acdc://same", SourceTitle: "S"},
	}}
	hybrid, err := NewHybridService(lexical, fusionEmbedder(), testSemanticSettings())
	require.NoError(t, err)
	t.Cleanup(hybrid.Close)
	require.NoError(t, indexHybrid(t, hybrid, chunks))

	for i := 0; i < 20; i++ {
		results, err := hybrid.Search("alpha", 10)

		require.NoError(t, err)
		require.Equal(t, []string{"c1", "c2"}, resultIDs(results),
			"tied chunks of one document order by ascending ChunkID")
	}
}

// --- malformed fakes ---

// malformedEmbedder breaks the Embedder contract in one chosen way, so Index
// can be asserted against an adapter that lies about its own output.
type malformedEmbedder struct {
	dimensions int
	// vectorWidth is the width actually returned, which may disagree with
	// Info().Dimensions.
	vectorWidth int
	// dropOne returns one fewer vector than there were texts.
	dropOne bool
}

func (m *malformedEmbedder) Info() embed.ModelInfo {
	return embed.ModelInfo{Dimensions: m.dimensions}
}

func (m *malformedEmbedder) EmbedDocuments(_ context.Context, texts []string) ([][]float32, error) {
	count := len(texts)
	if m.dropOne && count > 0 {
		count--
	}
	vectors := make([][]float32, count)
	for i := range vectors {
		vectors[i] = m.vector(m.vectorWidth)
	}
	return vectors, nil
}

func (m *malformedEmbedder) EmbedQuery(_ context.Context, _ string) ([]float32, error) {
	return m.vector(m.dimensions), nil
}

func (m *malformedEmbedder) vector(width int) []float32 {
	vector := make([]float32, width)
	if width > 0 {
		vector[0] = 1
	}
	return vector
}

// mutableEmbedder swaps its backing model between Index passes. Method
// promotion resolves the embedded interface at call time, so a reassignment
// takes effect on the next call.
type mutableEmbedder struct {
	embed.Embedder
}

func TestHybridService_IndexReportsThePublishedGeneration(t *testing.T) {
	read := captureLogs(t, slog.LevelInfo)
	hybrid, _ := newTestHybrid(t, embed.NewFake(8))

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))

	record := requireLog(t, read(), "Semantic index published")
	require.EqualValues(t, 2, record.Attrs["vectors"])
	require.EqualValues(t, 2, record.Attrs["passages_embedded"])
	require.EqualValues(t, 0, record.Attrs["passages_reused"])
	require.Contains(t, record.Attrs, "duration_ms")
}

// The carry-forward cache is the only reason a refresh is cheap, so the split
// between inferred and reused passages has to be measurable.
func TestHybridService_IndexReportsCarriedPassagesAsReused(t *testing.T) {
	hybrid, _ := newTestHybrid(t, embed.NewFake(8))
	require.NoError(t, indexHybrid(t, hybrid, testChunks()))

	read := captureLogs(t, slog.LevelInfo)
	require.NoError(t, indexHybrid(t, hybrid, testChunks()))

	record := requireLog(t, read(), "Semantic index published")
	require.EqualValues(t, 0, record.Attrs["passages_embedded"])
	require.EqualValues(t, 2, record.Attrs["passages_reused"])
}

func TestHybridService_IndexWarnsWhenTheGenerationHoldsNoVectors(t *testing.T) {
	fake := embed.NewFake(8)
	fake.MaxTokens = 1
	read := captureLogs(t, slog.LevelInfo)
	hybrid, _ := newTestHybrid(t, fake)

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))

	require.Equal(t, 0, hybrid.VectorCount())
	requireLog(t, read(), "Semantic index is empty, searches serve lexical results only")
}

func TestHybridService_IndexDoesNotWarnWhenVectorsWerePublished(t *testing.T) {
	read := captureLogs(t, slog.LevelInfo)
	hybrid, _ := newTestHybrid(t, embed.NewFake(8))

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))

	requireNoLog(t, read(), "Semantic index is empty, searches serve lexical results only")
}

// A count alone cannot be acted on: an operator needs to know which documents
// lost their vectors.
func TestHybridService_IndexNamesTheChunksItSkipped(t *testing.T) {
	fake := embed.NewFake(8)
	fake.MaxTokens = 1
	read := captureLogs(t, slog.LevelInfo)
	hybrid, _ := newTestHybrid(t, fake)

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))

	record := requireLog(t, read(), "Semantic indexing skipped passages that could not be embedded")
	require.EqualValues(t, 2, record.Attrs["chunks_affected"])
	require.ElementsMatch(t, []any{"c1", "c2"}, record.Attrs["chunks"])
	// Halving splits one oversized passage into many fragments, so the raw
	// drop count exceeds the number of chunks that lost vectors.
	require.Greater(t, record.Attrs["passages"], float64(0))
}

// The similarity floor is a placeholder until a real model calibrates it.
// Calibration needs the scores the floor rejected, so the debug line carries
// the best raw score whether or not it cleared.
func TestHybridService_SearchReportsRankingDetailAtDebug(t *testing.T) {
	read := captureLogs(t, slog.LevelDebug)
	hybrid, _ := newTestHybrid(t, fusionEmbedder())
	require.NoError(t, indexHybrid(t, hybrid, fusionChunks()))

	_, err := hybrid.Search("alpha", 10)
	require.NoError(t, err)

	record := requireLog(t, read(), "Semantic query ranked")
	require.EqualValues(t, 3, record.Attrs["candidates"])
	require.EqualValues(t, 2, record.Attrs["above_floor"])
	require.EqualValues(t, 1, record.Attrs["below_floor"])
	require.InDelta(t, 1.0, record.Attrs["top_score"], 1e-6)
	require.InDelta(t, config.DefaultSemanticFloor, record.Attrs["floor"], 1e-9)
}

func TestHybridService_SearchReportsTheTopScoreWhenNothingClearsTheFloor(t *testing.T) {
	read := captureLogs(t, slog.LevelDebug)
	hybrid, _ := newTestHybrid(t, fusionEmbedder())
	require.NoError(t, indexHybrid(t, hybrid, fusionChunks()))

	_, err := hybrid.Search("unscripted", 10)
	require.NoError(t, err)

	record := requireLog(t, read(), "Semantic query ranked")
	require.EqualValues(t, 0, record.Attrs["above_floor"])
	require.EqualValues(t, 3, record.Attrs["below_floor"])
	require.InDelta(t, 0.0, record.Attrs["top_score"], 1e-6)
}

// Per-query detail is high volume on a busy server, so the default level must
// not carry it.
func TestHybridService_SearchKeepsRankingDetailOffTheDefaultLevel(t *testing.T) {
	read := captureLogs(t, slog.LevelInfo)
	hybrid, _ := newTestHybrid(t, fusionEmbedder())
	require.NoError(t, indexHybrid(t, hybrid, fusionChunks()))

	_, err := hybrid.Search("alpha", 10)
	require.NoError(t, err)

	requireNoLog(t, read(), "Semantic query ranked")
}

// blockingRetriever lets a test hold a publish open and observe what a
// concurrent reader can see while both generations are half swapped.
type blockingRetriever struct {
	stubRetriever
	entered chan struct{}
	release chan struct{}
}

func (b *blockingRetriever) publish(generation *lexicalGeneration) {
	close(b.entered)
	<-b.release
	b.stubRetriever.publish(generation)
}

// The lexical index and the vector store are one generation. A reader must see
// both halves of the previous generation or both halves of the new one, never
// the new lexical index paired with the previous vectors.
func TestHybridService_SearchCannotObserveAHalfPublishedGeneration(t *testing.T) {
	lexical := &blockingRetriever{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	hybrid, err := NewHybridService(lexical, embed.NewFake(8), testSemanticSettings())
	require.NoError(t, err)
	t.Cleanup(hybrid.Close)

	// Registered after Close so it runs before it: a failing assertion below
	// would otherwise leave the publish holding the write lock, and Close would
	// block on it until the test binary times out instead of reporting.
	var release sync.Once
	unblock := func() { release.Do(func() { close(lexical.release) }) }
	t.Cleanup(unblock)

	indexed := make(chan error, 1)
	go func() { indexed <- indexHybrid(t, hybrid, testChunks()) }()

	<-lexical.entered

	searched := make(chan struct{})
	go func() {
		_, _ = hybrid.Search("alpha", 10)
		close(searched)
	}()

	select {
	case <-searched:
		t.Fatal("a search completed while a generation was half published")
	case <-time.After(50 * time.Millisecond):
	}

	unblock()
	require.NoError(t, <-indexed)

	select {
	case <-searched:
	case <-time.After(2 * time.Second):
		t.Fatal("the search never completed after the publish finished")
	}
}

// A vector build that fails must not hold back the lexical generation: the
// server still has to serve the content it just indexed.
func TestHybridService_IndexPublishesTheLexicalGenerationWhenVectorsFail(t *testing.T) {
	lexical := NewService(testSettings())
	hybrid, err := NewHybridService(lexical, &malformedEmbedder{dimensions: 8}, testSemanticSettings())
	require.NoError(t, err)
	t.Cleanup(hybrid.Close)

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))

	require.Equal(t, 0, hybrid.VectorCount(), "a failed vector build publishes no vectors")
	count, err := lexical.DocCount()
	require.NoError(t, err)
	require.Equal(t, uint64(2), count, "the lexical generation must still be published")
}

// slowEmbedder spends a measurable amount of time inside EmbedDocuments so a
// test can tell embedding cost apart from total rebuild time.
type slowEmbedder struct {
	embed.Embedder
	delay time.Duration
}

func (s *slowEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	time.Sleep(s.delay)
	return s.Embedder.EmbedDocuments(ctx, texts)
}

// duration_ms covers a rebuild whose two halves run concurrently, so it cannot
// answer what the embedder cost. embed_ms is measured inside the embedder and
// can.
func TestHybridService_IndexReportsTimeSpentEmbedding(t *testing.T) {
	read := captureLogs(t, slog.LevelInfo)
	embedder := &slowEmbedder{Embedder: embed.NewFake(8), delay: 25 * time.Millisecond}
	hybrid, _ := newTestHybrid(t, embedder)

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))

	record := requireLog(t, read(), "Semantic index published")
	require.GreaterOrEqual(t, record.Attrs["embed_ms"], float64(20))
}

// A rebuild that embeds nothing still reports, so a zero is a measurement and
// not a missing attribute.
func TestHybridService_IndexReportsNoEmbeddingTimeWhenEverythingIsCached(t *testing.T) {
	hybrid, _ := newTestHybrid(t, embed.NewFake(8))
	require.NoError(t, indexHybrid(t, hybrid, testChunks()))

	read := captureLogs(t, slog.LevelInfo)
	require.NoError(t, indexHybrid(t, hybrid, testChunks()))

	record := requireLog(t, read(), "Semantic index published")
	require.EqualValues(t, 0, record.Attrs["embed_ms"])
}

// A rebuild that is still embedding when Close lands must not install its
// vectors afterwards. The lexical side still receives the generation, because
// publish is what releases the built index and the rebuild lock.
func TestHybridService_PublishAfterCloseDropsTheGeneration(t *testing.T) {
	lexical := &stubRetriever{}
	hybrid, err := NewHybridService(lexical, embed.NewFake(8), testSemanticSettings())
	require.NoError(t, err)

	store := vector.NewStore(8)
	require.NoError(t, store.Add("c1", make([]float32, 8)))

	hybrid.Close()
	hybrid.publish(&lexicalGeneration{}, store, map[string][]float32{"p1": make([]float32, 8)})

	require.Equal(t, 1, lexical.published, "the built generation was never handed back for release")
	require.Zero(t, hybrid.VectorCount(), "vectors became visible after Close")
}

func newTestHybridWithFloor(t *testing.T, embedder embed.Embedder, floor float64) *HybridService {
	t.Helper()
	settings := testSemanticSettings()
	settings.SemanticFloor = floor
	lexical := NewService(testSettings())
	hybrid, err := NewHybridService(lexical, embedder, settings)
	require.NoError(t, err)
	t.Cleanup(hybrid.Close)
	return hybrid
}

// unrepresentableEmbedder answers with the zero vector for any text holding
// marker, which is what a static model does for text its vocabulary cannot
// cover, and with a real unit vector for everything else.
type unrepresentableEmbedder struct {
	dimensions int
	marker     string
}

func (u *unrepresentableEmbedder) Info() embed.ModelInfo {
	return embed.ModelInfo{Dimensions: u.dimensions}
}

func (u *unrepresentableEmbedder) vector(text string) []float32 {
	values := make([]float32, u.dimensions)
	if strings.Contains(text, u.marker) {
		return values
	}
	values[0] = 1
	return values
}

func (u *unrepresentableEmbedder) EmbedDocuments(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		vectors[i] = u.vector(text)
	}
	return vectors, nil
}

func (u *unrepresentableEmbedder) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	return u.vector(text), nil
}

// zeroQueryEmbedder represents every passage but no query at all. Keying this
// on the method rather than on the text is what keeps the query-side case
// unambiguous: every fixture query also appears in a chunk.
type zeroQueryEmbedder struct{ dimensions int }

func (z *zeroQueryEmbedder) Info() embed.ModelInfo {
	return embed.ModelInfo{Dimensions: z.dimensions}
}

func (z *zeroQueryEmbedder) EmbedDocuments(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i := range texts {
		vectors[i] = make([]float32, z.dimensions)
		vectors[i][0] = 1
	}
	return vectors, nil
}

func (z *zeroQueryEmbedder) EmbedQuery(context.Context, string) ([]float32, error) {
	return make([]float32, z.dimensions), nil
}

// A passage the embedder cannot represent must not reach the store. Its dot
// product is 0.0 against every query, so at a floor of zero it would clear the
// floor for every search and become a permanent hit.
func TestHybridService_SkipsPassagesTheEmbedderCannotRepresent(t *testing.T) {
	logs := captureLogs(t, slog.LevelWarn)
	hybrid := newTestHybridWithFloor(t, &unrepresentableEmbedder{dimensions: 4, marker: "beta"}, 0)

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))

	require.Equal(t, 1, hybrid.VectorCount(), "the unrepresentable passage must not be stored")
	record := requireLog(t, logs(), "Semantic indexing skipped passages that could not be embedded")
	// Attrs comes back through a JSON handler, so numbers arrive as float64.
	require.Equal(t, float64(1), record.Attrs["passages"], "the skip must be counted for the operator")
}

// A query the embedder cannot represent carries no semantic signal. Searching
// on it would rank the whole corpus by ties at 0.0, broken by chunk ID.
func TestHybridService_ZeroQueryVectorDegradesToLexical(t *testing.T) {
	logs := captureLogs(t, slog.LevelWarn)
	hybrid := newTestHybridWithFloor(t, &zeroQueryEmbedder{dimensions: 4}, 0)
	lexicalOnly := NewService(testSettings())
	t.Cleanup(lexicalOnly.Close)

	require.NoError(t, indexHybrid(t, hybrid, testChunks()))
	indexChunks(t, lexicalOnly, testChunks())

	fused, err := hybrid.Search("alpha content", 10)
	require.NoError(t, err)
	lexical, err := lexicalOnly.Search("alpha content", 10)
	require.NoError(t, err)

	require.Equal(t, lexical, fused, "a query with no embedding must return the lexical ranking unchanged")
	// Nothing failed — the embedder answered — so warning here would be false.
	requireNoLog(t, logs(), "Semantic query embedding failed, returning lexical results only")
}
