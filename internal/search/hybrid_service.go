package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

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
	// build and publish are the two halves of Index. HybridService needs them
	// apart so the lexical index and the vector store become visible in the
	// same instant. They are unexported, so only a lexical retriever in this
	// package can satisfy the contract.
	build(ctx context.Context, chunks <-chan domain.Chunk) (*lexicalGeneration, error)
	publish(generation *lexicalGeneration)
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
	// closed records that Close has run, so a rebuild still embedding at
	// shutdown does not install its vectors afterwards.
	closed bool
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
//
// The read lock spans both retrievers, which is what makes a generation
// atomic. Locking each side as it is read would let a rebuild land between the
// two reads and pair a new lexical index with the vectors of the generation
// before it. The lock is held across the bleve query and the embedder call, so
// it must never be taken again further down: sync.RWMutex is not reentrant,
// and a writer arriving between two read locks deadlocks the reader.
func (h *HybridService) Search(query string, candidateLimit int) ([]SearchResult, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// The raw limit, not the resolved one: the lexical side applies its own
	// established fallback, and imposing a second one here would override it.
	lexical, err := h.lexical.Search(query, candidateLimit)
	if err != nil {
		return nil, err
	}

	// D11: the semantic side cannot distinguish "no answer exists" from "an
	// answer exists", measured over the real corpus; the lexical side can. An
	// empty lexical result is the one honest signal that a query's terms
	// appear nowhere, so it stands rather than being filled with the corpus's
	// nearest-but-irrelevant chunks.
	if len(lexical) == 0 {
		return lexical, nil
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
//
// The caller holds the read lock. See Search.
func (h *HybridService) vectorHits(query string, limit int) []vector.Hit {
	// "*" is the lexical match-all sentinel. Embedding it would rank the whole
	// corpus by its similarity to an asterisk.
	if query == "*" {
		return nil
	}

	store := h.vectors
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
		slog.Warn("Semantic query embedding failed, returning lexical results only", "error", err)
		return nil
	}

	// No semantic signal at all. This is not a failure — the embedder answered
	// — so it degrades to lexical silently, unlike the embedding-error path
	// above, which warns.
	if vector.IsZero(queryVector) {
		return nil
	}

	scanned := store.Search(queryVector, limit)
	hits := make([]vector.Hit, 0, len(scanned))
	for _, hit := range scanned {
		if hit.Score >= h.floor {
			hits = append(hits, hit)
		}
	}
	h.reportRanking(scanned, len(hits))

	return hits
}

// reportRanking records what the floor admitted and what it rejected.
//
// The top score is the best raw similarity the scan found, reported whether or
// not it cleared the floor. Without it a floor set too high is indistinguishable
// from a corpus with nothing to say, and neither the placeholder default nor any
// later value can be calibrated against a real corpus.
//
// This is debug rather than info because it fires once per query.
func (h *HybridService) reportRanking(scanned []vector.Hit, admitted int) {
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		return
	}

	top := 0.0
	if len(scanned) > 0 {
		// Store.Search returns hits in descending score order.
		top = scanned[0].Score
	}

	slog.Debug("Semantic query ranked",
		"candidates", len(scanned),
		"above_floor", admitted,
		"below_floor", len(scanned)-admitted,
		"top_score", top,
		"floor", h.floor)
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
// A lexical failure fails the rebuild. A vector failure does not: the lexical
// generation is published with no vectors beside it, so this generation serves
// lexical results only. A vector index whose chunk IDs disagree with the
// lexical index is worse than none.
//
// Nothing is published until both sides have reported, because the two failure
// modes leave different survivors. The lexical build keeps its previous index
// on failure and catalogRefresher.Revalidate keeps its previous catalog, so the
// vectors already published are exactly the generation that survived: they are
// retained untouched rather than cleared.
func (h *HybridService) Index(ctx context.Context, chunks <-chan domain.Chunk) error {
	indexCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	lexicalChunks := make(chan domain.Chunk, teeBuffer)
	type lexicalResult struct {
		generation *lexicalGeneration
		err        error
	}
	lexicalResults := make(chan lexicalResult, 1)

	go func() {
		generation, err := h.lexical.build(indexCtx, lexicalChunks)
		if err != nil {
			cancel()
		}
		// Keep reading until close even after an error: the tee would block
		// forever otherwise, and with it the producer upstream.
		for range lexicalChunks {
		}
		lexicalResults <- lexicalResult{generation: generation, err: err}
	}()

	started := time.Now()
	store, cache, stats, buildErr := h.buildVectors(indexCtx, chunks, lexicalChunks)
	lexical := <-lexicalResults

	if lexical.err != nil {
		// Publish nothing and clear nothing. The retained bleve index and the
		// retained vector generation are the same generation, so they survive
		// or fall together; clearing would take semantic search dark until the
		// next successful reindex and then charge a full corpus re-embed.
		return lexical.err
	}
	if buildErr != nil {
		h.publish(lexical.generation, nil, nil)
		slog.Error("Semantic index build failed, this generation serves lexical results only", "error", buildErr)
		return nil
	}

	h.publish(lexical.generation, store, cache)
	reportPublished(store, stats, time.Since(started))

	return nil
}

// publish makes the lexical index and the vector store visible together.
//
// Both swaps happen under this one write lock, and Search reads both under the
// matching read lock, so no reader can pair one generation's lexical index with
// another's vectors. The lock order is always hybrid then lexical; Search takes
// them in the same order, so the two cannot invert.
func (h *HybridService) publish(generation *lexicalGeneration, store *vector.Store, cache map[string][]float32) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// The generation goes to the lexical side even after Close, because publish
	// is what releases the built index and the rebuild lock. The lexical side
	// records its own shutdown and releases rather than installs.
	h.lexical.publish(generation)
	if h.closed {
		return
	}
	h.vectors = store
	h.cache = cache
}

// reportPublished records what the generation cost and whether it can answer
// anything. An empty store is not an error: a corpus can legitimately hold no
// embeddable passage. It is still worth a warning, because every later query
// silently serves lexical results only, and nothing else would say so.
func reportPublished(store *vector.Store, stats buildStats, elapsed time.Duration) {
	slog.Info("Semantic index published",
		"vectors", store.Len(),
		"passages_embedded", stats.embedded,
		"passages_reused", stats.reused,
		"embed_ms", stats.embedding.Milliseconds(),
		"duration_ms", elapsed.Milliseconds())

	if store.Len() == 0 {
		slog.Warn("Semantic index is empty, searches serve lexical results only")
	}
}

// buildVectors forwards every chunk to the lexical side while embedding its
// passages, and returns the completed generation for its caller to publish. It
// always drains its input and always closes its output, whatever fails, because
// the producer upstream is blocked on a bounded channel.
func (h *HybridService) buildVectors(
	ctx context.Context,
	in <-chan domain.Chunk,
	out chan<- domain.Chunk,
) (*vector.Store, map[string][]float32, buildStats, error) {
	defer close(out)

	info := h.embedder.Info()
	model := vector.Model{ID: h.modelID, Dimensions: info.Dimensions, MaxTokens: info.MaxTokens}
	previous := h.snapshotCache()
	next := make(map[string][]float32, len(previous))
	store := vector.NewStore(info.Dimensions)

	var pending []vector.Passage
	var buildErr error
	var stats buildStats

	flush := func() {
		if buildErr != nil || len(pending) == 0 {
			pending = pending[:0]
			return
		}
		if err := h.embedBatch(ctx, pending, previous, next, store, &stats); err != nil {
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
		return nil, nil, stats, buildErr
	}
	if stats.skipped > 0 {
		slog.Warn("Semantic indexing skipped passages that could not be embedded",
			"passages", stats.skipped,
			"chunks_affected", len(stats.skippedChunks),
			"chunks", stats.loggedSkippedChunks())
	}
	return store, next, stats, nil
}

// maxLoggedSkippedChunks bounds the chunk list in the skip warning. A corpus
// that fails wholesale would otherwise put every chunk ID on one line; the
// count attribute stays exact regardless.
const maxLoggedSkippedChunks = 20

// buildStats is what one vector generation cost, for the line Index logs when
// it publishes. The embedded/reused split is the only measure of whether the
// carry-forward cache is working.
type buildStats struct {
	embedded int
	reused   int
	skipped  int
	// embedding is the time spent inside the embedder. The rebuild's total
	// wall clock covers the lexical build running concurrently, so it cannot
	// answer what inference cost.
	embedding time.Duration
	// skippedChunks are the distinct chunks that lost passages, in the order
	// they were skipped.
	skippedChunks []string
	seenSkipped   map[string]struct{}
}

func (b *buildStats) skip(owners []string) {
	b.skipped++
	if b.seenSkipped == nil {
		b.seenSkipped = make(map[string]struct{}, len(owners))
	}
	for _, owner := range owners {
		if _, seen := b.seenSkipped[owner]; seen {
			continue
		}
		b.seenSkipped[owner] = struct{}{}
		b.skippedChunks = append(b.skippedChunks, owner)
	}
}

// loggedSkippedChunks is the bounded chunk list for the skip warning.
func (b *buildStats) loggedSkippedChunks() []string {
	if len(b.skippedChunks) <= maxLoggedSkippedChunks {
		return b.skippedChunks
	}
	return b.skippedChunks[:maxLoggedSkippedChunks]
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
	stats *buildStats,
) error {
	queue := pendingQueue(passages)

	for len(queue) > 0 {
		// Resolved before every call, not once up front: halving mints passages
		// a previous generation may already have paid for, and those are the
		// most expensive passages in the corpus to re-embed.
		unresolved, err := drainCached(queue, previous, next, store, stats)
		if err != nil {
			return err
		}
		queue = unresolved
		if len(queue) == 0 {
			break
		}

		texts := make([]string, len(queue))
		for i, item := range queue {
			texts[i] = item.passage.Text
		}

		startedEmbedding := time.Now()
		vectors, embedErr := h.embedder.EmbedDocuments(ctx, texts)
		stats.embedding += time.Since(startedEmbedding)
		if embedErr == nil {
			if len(vectors) != len(queue) {
				return fmt.Errorf("embedder returned %d vectors for %d texts", len(vectors), len(queue))
			}

			return recordEmbedded(queue, vectors, next, store, stats)
		}

		shortened, droppedOwners, retryErr := retryAfterTooLong(queue, embedErr)
		if retryErr != nil {
			return retryErr
		}
		queue = shortened
		if droppedOwners != nil {
			stats.skip(droppedOwners)
		}
	}

	return nil
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
	stats *buildStats,
) ([]pendingPassage, error) {
	unresolved := queue[:0]
	for _, item := range queue {
		cached, ok := cachedVector(item.passage.Key, previous, next)
		if !ok {
			unresolved = append(unresolved, item)
			continue
		}
		stats.reused++
		// Dedup the inference, not the vector: each owning chunk needs one.
		if err := storePassage(store, item, cached, stats); err != nil {
			return nil, err
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
	stats *buildStats,
) error {
	stats.embedded += len(queue)
	for i, item := range queue {
		// Cache what the embedder answered, zero vector included: re-embedding
		// an unrepresentable passage every generation would buy nothing.
		next[item.passage.Key] = vectors[i]
		if err := storePassage(store, item, vectors[i], stats); err != nil {
			return err
		}
	}

	return nil
}

// storePassage adds one passage vector under each owning chunk, unless the
// embedder could not represent the text.
//
// A skipped passage is counted, so an operator whose corpus the model cannot
// represent — a script outside its vocabulary, for instance — sees it in the
// same summary warning as a passage that failed to embed. That is what it is.
func storePassage(store *vector.Store, item pendingPassage, embedding []float32, stats *buildStats) error {
	if vector.IsZero(embedding) {
		stats.skip(item.owners)
		return nil
	}
	for _, owner := range item.owners {
		if err := store.Add(owner, embedding); err != nil {
			return err
		}
	}

	return nil
}

// retryAfterTooLong reshapes the queue around an over-length input, returning
// the owning chunks of the passage it dropped rather than halved, or nil when
// it halved. Any error the caller cannot retry is returned unchanged.
func retryAfterTooLong(queue []pendingPassage, err error) ([]pendingPassage, []string, error) {
	var tooLong *embed.ErrInputTooLong
	if !errors.As(err, &tooLong) {
		return nil, nil, err
	}
	// A reported index outside the batch is a contract violation by the
	// adapter, not a data condition: there is no passage to halve or skip,
	// and an adapter that misreports indices cannot be trusted about any
	// index. Fail the whole vector generation rather than guess — Index
	// degrades to lexical-only, which is uniform and visible in the logs,
	// whereas a partially embedded corpus fails silently per chunk.
	if tooLong.Index < 0 || tooLong.Index >= len(queue) {
		return nil, nil, fmt.Errorf("embedder reported input %d for a batch of %d: %w", tooLong.Index, len(queue), err)
	}

	item := queue[tooLong.Index]
	first, second, divisible := item.passage.Halve()
	if !divisible || item.depth >= vector.MaxHalveDepth {
		return append(queue[:tooLong.Index], queue[tooLong.Index+1:]...), item.owners, nil
	}

	queue[tooLong.Index] = pendingPassage{passage: first, owners: item.owners, depth: item.depth + 1}

	return append(queue, pendingPassage{passage: second, owners: item.owners, depth: item.depth + 1}), nil, nil
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
//
// The write lock covers both, so a search already in flight finishes against a
// whole generation before either half is released. A rebuild still embedding
// when Close lands is not waited for: it drops its generation when it reaches
// publish.
func (h *HybridService) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.closed = true
	h.lexical.Close()
	h.vectors = nil
	h.cache = nil
}
