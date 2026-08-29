package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/embed"
	"github.com/sha1n/mcp-acdc-server/internal/search/vector"
)

// textRetriever is what HybridService needs from its lexical side.
//
// It is declared here, where it is consumed, rather than on Searcher: Searcher
// is the shared contract app.IndexResources and mcp.fillSearchPage speak, and
// widening it would force HybridService to carry a Fetch nobody calls and
// every test fake to grow a method.
type textRetriever interface {
	Searcher
	Fetch(ids []string) ([]SearchResult, error)
}

// teeBuffer matches the buffer IndexResources uses upstream, so the tee adds
// no new backpressure characteristic to the pipeline.
const teeBuffer = 100

// embedBatchSize is how many passages accumulate before one EmbedDocuments
// call. Index-time embedding is throughput-bound, so batching matters; the
// value is small enough that a too-long error costs little to re-split.
const embedBatchSize = 64

// HybridService decorates a lexical retriever with semantic retrieval and
// fuses the two rankings.
type HybridService struct {
	mu       sync.RWMutex
	lexical  textRetriever
	embedder embed.Embedder
	modelID  string
	// floor is the minimum cosine similarity a vector hit must clear to enter
	// fusion. Configured rather than constant: see NewHybridService.
	floor float64
	// maxResults is the settings fallback applied when a caller passes a
	// non-positive candidate limit, mirroring TextService.Search.
	maxResults int
	vectors    *vector.Store
	// cache maps a passage key to its vector for the current generation. It is
	// replaced wholesale at swap time so vectors for deleted documents are not
	// retained forever.
	cache map[string][]float32
}

// Ensure HybridService implements Searcher
var _ Searcher = (*HybridService)(nil)

// NewHybridService decorates lexical with semantic retrieval.
//
// settings.SemanticModel salts the passage cache key, and
// settings.SemanticFloor is the similarity floor. The embedder is owned by the
// caller and lives for the process lifetime; Close does not release it.
//
// The lexical side must provide Fetch. That is checked here rather than in the
// type system because Fetch deliberately stays off the Searcher interface;
// *TextService satisfies it for free, and there is exactly one production
// call site.
func NewHybridService(lexical Searcher, embedder embed.Embedder, settings config.SearchSettings) (*HybridService, error) {
	retriever, ok := lexical.(textRetriever)
	if !ok {
		return nil, fmt.Errorf("semantic search requires a lexical retriever providing Fetch, got %T", lexical)
	}
	if embedder == nil {
		return nil, errors.New("semantic search requires an embedder")
	}
	return &HybridService{
		lexical:    retriever,
		embedder:   embedder,
		modelID:    settings.SemanticModel,
		floor:      settings.SemanticFloor,
		maxResults: settings.MaxResults,
	}, nil
}

// rrfK is the Reciprocal Rank Fusion constant from Cormack et al. RRF combines
// ranks rather than scores, which is what lets BM25 and cosine — on
// incomparable scales — be merged with no tuning.
//
// Unlike the similarity floor this is not configurable: it is a property of the
// fusion algorithm, not of any model or corpus.
const rrfK = 60.0

// Search fuses lexical and semantic rankings.
//
// Searcher carries no context, so query embedding runs on a background
// context. A query-time embedding failure degrades to lexical results rather
// than failing the request.
func (h *HybridService) Search(query string, candidateLimit int) ([]SearchResult, error) {
	// The raw limit, not the resolved one: the lexical side applies its own
	// established fallback, and imposing a second one here would override it.
	lexical, err := h.lexical.Search(query, candidateLimit)
	if err != nil {
		return nil, err
	}

	limit := h.effectiveLimit(candidateLimit)
	hits := h.vectorHits(query, limit)
	if len(hits) == 0 {
		return lexical, nil
	}
	return h.fuse(lexical, hits, limit)
}

// effectiveLimit resolves how many results fusion may return.
//
// A hybrid retriever is a Searcher like any other and must not answer a
// non-positive limit differently from the lexical one, which falls back to
// settings.MaxResults. Without this the two implementations diverge on
// identical input: one caps at MaxResults, the other scans and returns the
// whole corpus.
func (h *HybridService) effectiveLimit(candidateLimit int) int {
	if candidateLimit > 0 {
		return candidateLimit
	}
	return h.maxResults
}

// vectorHits scans the store and drops everything below the configured floor.
//
// Cosine always ranks something first, so without a floor a query with no real
// match would return the corpus's nearest-but-irrelevant chunks where the
// server returns "No results found" today. The floor's default is a
// placeholder until the adapter plan calibrates it, which is exactly why it is
// configurable: no value here is right for every model and corpus.
func (h *HybridService) vectorHits(query string, limit int) []vector.Hit {
	// "*" is the lexical match-all sentinel. Embedding it would rank the whole
	// corpus by its similarity to an asterisk.
	if query == "*" {
		return nil
	}

	h.mu.RLock()
	store := h.vectors
	h.mu.RUnlock()

	if store == nil || store.Len() == 0 {
		return nil
	}

	// A non-positive limit survives effectiveLimit only when MaxResults is
	// itself non-positive, which is the lexical side's "return nothing" signal:
	// TextService.Search hands bleve a Size of 0. The semantic side must not
	// answer a question the lexical side declined, so it contributes nothing
	// rather than scanning the whole store.
	if limit <= 0 {
		return nil
	}

	queryVector, err := h.embedder.EmbedQuery(context.Background(), query)
	if err != nil {
		slog.Warn("semantic query embedding failed; returning lexical results only", "error", err)
		return nil
	}

	scanned := store.Search(queryVector, limit)
	hits := make([]vector.Hit, 0, len(scanned))
	for _, hit := range scanned {
		if hit.Score >= h.floor {
			hits = append(hits, hit)
		}
	}
	return hits
}

// fuse accumulates a reciprocal per ranking, hydrates chunks only the vector
// side found, and rescales so the top result is 1.
func (h *HybridService) fuse(lexical []SearchResult, hits []vector.Hit, limit int) ([]SearchResult, error) {
	scores := make(map[string]float64, len(lexical)+len(hits))
	byID := make(map[string]SearchResult, len(lexical)+len(hits))

	for rank, result := range lexical {
		scores[result.ChunkID] += reciprocalRank(rank)
		byID[result.ChunkID] = result
	}

	var missing []string
	for rank, hit := range hits {
		scores[hit.ChunkID] += reciprocalRank(rank)
		if _, known := byID[hit.ChunkID]; !known {
			missing = append(missing, hit.ChunkID)
		}
	}

	if err := h.hydrate(byID, missing); err != nil {
		return nil, err
	}

	fused := rankFused(scores, byID)
	if limit > 0 && len(fused) > limit {
		fused = fused[:limit]
	}
	normalizeScores(fused)
	return fused, nil
}

// hydrate fills byID with the chunks only the vector side found.
func (h *HybridService) hydrate(byID map[string]SearchResult, missing []string) error {
	if len(missing) == 0 {
		return nil
	}

	fetched, err := h.lexical.Fetch(missing)
	if err != nil {
		return fmt.Errorf("failed to hydrate semantic results: %w", err)
	}
	for _, result := range fetched {
		byID[result.ChunkID] = result
	}

	return nil
}

// rankFused projects scored chunk IDs onto their results, in the same order
// TextService.Search applies so results stay stable for the golden harness.
func rankFused(scores map[string]float64, byID map[string]SearchResult) []SearchResult {
	fused := make([]SearchResult, 0, len(scores))
	for chunkID, score := range scores {
		result, known := byID[chunkID]
		if !known {
			// A vector survives for a chunk the lexical index no longer holds.
			// Returning it without metadata would be worse than dropping it.
			continue
		}
		result.Score = score
		fused = append(fused, result)
	}

	sort.Slice(fused, func(i, j int) bool {
		if fused[i].Score != fused[j].Score {
			return fused[i].Score > fused[j].Score
		}
		if fused[i].SourceURI != fused[j].SourceURI {
			return fused[i].SourceURI < fused[j].SourceURI
		}
		return fused[i].ChunkID < fused[j].ChunkID
	})

	return fused
}

func reciprocalRank(rank int) float64 {
	return 1 / (rrfK + float64(rank) + 1)
}

// normalizeScores rescales fused scores so the top result is 1.
//
// Raw RRF scores cluster near 1/60, and mcp.formatSearchResults renders
// "score %.2f", so every fused result would print as 0.02 or 0.03. Widening
// that format was the alternative, but it would change the lexical-only
// output too, and semantic search is meant to cost nothing when off. RRF ranks
// carry meaning; absolute RRF values do not, so rescaling loses nothing.
func normalizeScores(results []SearchResult) {
	if len(results) == 0 || results[0].Score <= 0 {
		return
	}
	top := results[0].Score
	for i := range results {
		results[i].Score /= top
	}
}

// Index rebuilds both indexes from one chunk stream.
//
// A lexical failure fails the rebuild. A vector failure does not: it clears
// the store and logs, so this generation serves lexical results only. A vector
// index whose chunk IDs disagree with the lexical index is worse than none.
//
// Nothing is published until both sides have reported, because the two failure
// modes leave different survivors. TextService.Index keeps its previous index
// on failure and catalogRefresher.Revalidate keeps its previous catalog, so the
// vectors already published are exactly the generation that survived: they are
// retained untouched rather than cleared.
func (h *HybridService) Index(ctx context.Context, chunks <-chan domain.Chunk) error {
	indexCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	lexicalChunks := make(chan domain.Chunk, teeBuffer)
	lexicalErrs := make(chan error, 1)

	go func() {
		err := h.lexical.Index(indexCtx, lexicalChunks)
		if err != nil {
			cancel()
		}
		// Keep reading until close even after an error: the tee would block
		// forever otherwise, and with it the producer upstream.
		for range lexicalChunks {
		}
		lexicalErrs <- err
	}()

	store, cache, buildErr := h.buildVectors(indexCtx, chunks, lexicalChunks)
	lexicalErr := <-lexicalErrs

	// KNOWN LIMITATION: the two generations do not publish atomically.
	// TextService.Index publishes and unlocks the new bleve index before this
	// goroutine reports, so a concurrent Search can pair the new lexical
	// generation with the previous vector generation. What bounds it: Fetch
	// hydrates from the current lexical index, so no stale metadata is ever
	// returned, and fuse drops vector hits for chunks that index no longer
	// holds. Only ranking is affected, for a sub-second window during a live
	// refresh. Closing it takes a two-phase publish in TextService so both
	// generations swap under one lock, and must happen before semantic search
	// is enabled in production.
	if lexicalErr != nil {
		// Publish nothing and clear nothing. The retained bleve index and the
		// retained vector generation are the same generation, so they survive
		// or fall together; clearing would take semantic search dark until the
		// next successful reindex and then charge a full corpus re-embed.
		return lexicalErr
	}
	if buildErr != nil {
		h.swap(nil, nil)
		slog.Error("semantic index build failed; this generation serves lexical results only", "error", buildErr)
		return nil
	}
	h.swap(store, cache)
	return nil
}

// buildVectors forwards every chunk to the lexical side while embedding its
// passages, and returns the completed generation for its caller to publish. It
// always drains its input and always closes its output, whatever fails, because
// the producer upstream is blocked on a bounded channel.
func (h *HybridService) buildVectors(
	ctx context.Context,
	in <-chan domain.Chunk,
	out chan<- domain.Chunk,
) (*vector.Store, map[string][]float32, error) {
	defer close(out)

	info := h.embedder.Info()
	model := vector.Model{ID: h.modelID, Dimensions: info.Dimensions, MaxTokens: info.MaxTokens}
	previous := h.snapshotCache()
	next := make(map[string][]float32, len(previous))
	store := vector.NewStore(info.Dimensions)

	var pending []vector.Passage
	var buildErr error
	skipped := 0

	flush := func() {
		if buildErr != nil || len(pending) == 0 {
			pending = pending[:0]
			return
		}
		batchSkipped, err := h.embedBatch(ctx, pending, previous, next, store)
		skipped += batchSkipped
		if err != nil {
			buildErr = err
		}
		pending = pending[:0]
	}

	for chunk := range in {
		select {
		case out <- chunk:
		case <-ctx.Done():
			if buildErr == nil {
				buildErr = ctx.Err()
			}
		}
		if buildErr != nil {
			continue
		}
		pending = append(pending, vector.BuildPassages(chunk, model)...)
		if len(pending) >= embedBatchSize {
			flush()
		}
	}
	flush()

	if buildErr != nil {
		return nil, nil, buildErr
	}
	if skipped > 0 {
		slog.Warn("semantic indexing skipped passages that could not be embedded", "count", skipped)
	}
	return store, next, nil
}

// pendingPassage tracks how many times a passage has been halved, so a model
// whose real tokenizer disagrees with the rune estimate cannot drive the
// retry loop forever.
type pendingPassage struct {
	passage vector.Passage
	// owners are every chunk this passage text belongs to: identical passages
	// across documents embed once but are stored per owning chunk, so a
	// halved passage's owners follow both halves and a skipped one is counted
	// once, not once per owner.
	owners []string
	depth  int
}

// embedBatch resolves each passage's vector from the carry-forward cache, from
// this generation's own work, or from the embedder, and reports how many
// passages were skipped.
//
// Halving terminates because every retry either drops a passage or increases
// one passage's depth, and depth is bounded by vector.MaxHalveDepth.
func (h *HybridService) embedBatch(
	ctx context.Context,
	passages []vector.Passage,
	previous, next map[string][]float32,
	store *vector.Store,
) (int, error) {
	skipped := 0
	queue := pendingQueue(passages)

	for len(queue) > 0 {
		// Resolved before every call, not once up front: halving mints passages
		// a previous generation may already have paid for, and those are the
		// most expensive passages in the corpus to re-embed.
		unresolved, err := drainCached(queue, previous, next, store)
		if err != nil {
			return skipped, err
		}
		queue = unresolved
		if len(queue) == 0 {
			break
		}

		texts := make([]string, len(queue))
		for i, item := range queue {
			texts[i] = item.passage.Text
		}

		vectors, embedErr := h.embedder.EmbedDocuments(ctx, texts)
		if embedErr == nil {
			if len(vectors) != len(queue) {
				return skipped, fmt.Errorf("embedder returned %d vectors for %d texts", len(vectors), len(queue))
			}

			return skipped, recordEmbedded(queue, vectors, next, store)
		}

		shortened, dropped, retryErr := retryAfterTooLong(queue, embedErr)
		if retryErr != nil {
			return skipped, retryErr
		}
		queue = shortened
		if dropped {
			skipped++
		}
	}

	return skipped, nil
}

// pendingQueue collapses passages sharing a key into one queue entry, so an
// identical passage is inferred once however many chunks own it.
func pendingQueue(passages []vector.Passage) []pendingPassage {
	queue := make([]pendingPassage, 0, len(passages))
	// queued is valid only while the queue is being built; the retry loop in
	// embedBatch reorders and shrinks the queue, which would invalidate its
	// indices.
	queued := make(map[string]int, len(passages))

	for _, passage := range passages {
		if at, ok := queued[passage.Key]; ok {
			queue[at].owners = append(queue[at].owners, passage.ChunkID)
			continue
		}
		queued[passage.Key] = len(queue)
		queue = append(queue, pendingPassage{passage: passage, owners: []string{passage.ChunkID}})
	}

	return queue
}

// drainCached stores the vectors already available for the queue and returns
// the entries still needing inference. It filters in place.
func drainCached(
	queue []pendingPassage,
	previous, next map[string][]float32,
	store *vector.Store,
) ([]pendingPassage, error) {
	unresolved := queue[:0]
	for _, item := range queue {
		cached, ok := cachedVector(item.passage.Key, previous, next)
		if !ok {
			unresolved = append(unresolved, item)
			continue
		}
		// Dedup the inference, not the vector: each owning chunk needs one.
		for _, owner := range item.owners {
			if err := store.Add(owner, cached); err != nil {
				return nil, err
			}
		}
	}

	return unresolved, nil
}

// recordEmbedded caches each freshly inferred vector and stores a copy under
// every chunk that owns its passage.
func recordEmbedded(
	queue []pendingPassage,
	vectors [][]float32,
	next map[string][]float32,
	store *vector.Store,
) error {
	for i, item := range queue {
		next[item.passage.Key] = vectors[i]
		for _, owner := range item.owners {
			if err := store.Add(owner, vectors[i]); err != nil {
				return err
			}
		}
	}

	return nil
}

// retryAfterTooLong reshapes the queue around an over-length input, reporting
// whether the offending passage was dropped rather than halved. Any error the
// caller cannot retry is returned unchanged.
func retryAfterTooLong(queue []pendingPassage, err error) ([]pendingPassage, bool, error) {
	var tooLong *embed.ErrInputTooLong
	if !errors.As(err, &tooLong) {
		return nil, false, err
	}
	// A reported index outside the batch is a contract violation by the
	// adapter, not a data condition: there is no passage to halve or skip,
	// and an adapter that misreports indices cannot be trusted about any
	// index. Fail the whole vector generation rather than guess — Index
	// degrades to lexical-only, which is uniform and visible in the logs,
	// whereas a partially embedded corpus fails silently per chunk.
	if tooLong.Index < 0 || tooLong.Index >= len(queue) {
		return nil, false, fmt.Errorf("embedder reported input %d for a batch of %d: %w", tooLong.Index, len(queue), err)
	}

	item := queue[tooLong.Index]
	first, second, divisible := item.passage.Halve()
	if !divisible || item.depth >= vector.MaxHalveDepth {
		return append(queue[:tooLong.Index], queue[tooLong.Index+1:]...), true, nil
	}

	queue[tooLong.Index] = pendingPassage{passage: first, owners: item.owners, depth: item.depth + 1}

	return append(queue, pendingPassage{passage: second, owners: item.owners, depth: item.depth + 1}), false, nil
}

// cachedVector resolves a passage key against the previous generation's cache
// and this generation's own work. A previous-generation hit is recorded in next
// so the completed cache stays whole and survives the swap.
func cachedVector(key string, previous, next map[string][]float32) ([]float32, bool) {
	if carried, ok := previous[key]; ok {
		next[key] = carried
		return carried, true
	}
	embedded, ok := next[key]
	return embedded, ok
}

func (h *HybridService) snapshotCache() map[string][]float32 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cache
}

// swap publishes a completed generation and drops the previous one, mirroring
// the index swap in TextService.Index.
func (h *HybridService) swap(store *vector.Store, cache map[string][]float32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.vectors = store
	h.cache = cache
}

// VectorCount reports how many passage vectors are held, for tests and
// diagnostics.
func (h *HybridService) VectorCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.vectors == nil {
		return 0
	}
	return h.vectors.Len()
}

// Close releases the lexical index and drops the vector store. It does not
// close the embedder: factory.go constructs and owns it for the process
// lifetime.
func (h *HybridService) Close() {
	h.lexical.Close()
	h.swap(nil, nil)
}
