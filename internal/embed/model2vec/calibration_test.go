package model2vec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/embed"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/sha1n/mcp-acdc-server/internal/search/vector"
)

// sanityPair is one paraphrase query and the chunk that answers it. The query
// deliberately shares no distinctive content word with its chunk: one that
// repeated the chunk's vocabulary would measure the lexical retriever.
type sanityPair struct {
	Query string `json:"query"`
	Chunk string `json:"chunk"`
}

// sanitySet is a corpus and the paraphrase pairs measured over it.
type sanitySet struct {
	ContentDir string       `json:"content_dir"`
	Pairs      []sanityPair `json:"pairs"`
}

// candidateLimit is the per-side depth the pre-implementation benchmark used:
// top 10 from each retriever, fused with RRF k=60.
const candidateLimit = 10

// recallDepths are the ranks that benchmark reported, so the in-repo rows can
// be read against it column by column.
var recallDepths = []int{1, 3, 5, 10}

// TestCalibration_SemanticFloor reports the distributions
// config.DefaultSemanticFloor is chosen from. It is a measurement, not a gate:
// it asserts only that the model separates the sanity set at all, and logs
// everything else for a human to read.
//
//	ACDC_MCP_TEST_MODEL_DIR=/tmp/potion-retrieval-32M \
//	  go test ./internal/embed/model2vec/ -run Calibration -v
func TestCalibration_SemanticFloor(t *testing.T) {
	dir := os.Getenv("ACDC_MCP_TEST_MODEL_DIR")
	if dir == "" {
		t.Skip("set ACDC_MCP_TEST_MODEL_DIR to a potion model directory to calibrate")
	}
	quietLogging(t)
	set := loadSanitySet(t, envOr("ACDC_MCP_TEST_SANITY_SET", filepath.Join("testdata", "sanity-sample-content.json")))

	embedder, err := New(dir)
	if err != nil {
		t.Fatalf("New(%q): %v", dir, err)
	}

	// uris is sorted so the reported numbers are reproducible run to run.
	passages := loadPassages(t, set.ContentDir) // chunk URI -> its passage texts
	uris := slices.Sorted(maps.Keys(passages))
	index := make(map[string]int, len(uris))
	var texts []string
	var owners []int
	for i, uri := range uris {
		index[uri] = i
		for _, text := range passages[uri] {
			texts = append(texts, text)
			owners = append(owners, i)
		}
	}
	for _, pair := range set.Pairs {
		if _, ok := index[pair.Chunk]; !ok {
			t.Fatalf("the sanity set names a chunk no corpus passage carries: %q", pair.Chunk)
		}
	}

	started := time.Now()
	vectors, err := embedder.EmbedDocuments(context.Background(), texts)
	if err != nil {
		t.Fatalf("EmbedDocuments: %v", err)
	}
	corpusEmbed := time.Since(started)

	var matching, nonMatching []float64
	var hitsAt1, hitsAt5 int
	queryStarted := time.Now()
	for _, pair := range set.Pairs {
		query, err := embedder.EmbedQuery(context.Background(), pair.Query)
		if err != nil {
			t.Fatalf("EmbedQuery(%q): %v", pair.Query, err)
		}

		// Max-pool per chunk, exactly as vector.Store.Search does.
		scores := make([]float64, len(uris))
		for i := range scores {
			scores[i] = math.Inf(-1)
		}
		for i, vector := range vectors {
			if score := dot(query, vector); score > scores[owners[i]] {
				scores[owners[i]] = score
			}
		}

		want := index[pair.Chunk]
		matching = append(matching, scores[want])
		rank := 0
		for i, score := range scores {
			if i == want {
				continue
			}
			nonMatching = append(nonMatching, score)
			if score > scores[want] {
				rank++
			}
		}
		if rank == 0 {
			hitsAt1++
		}
		if rank < 5 {
			hitsAt5++
		}
	}
	queryLatency := time.Since(queryStarted) / time.Duration(len(set.Pairs))

	slices.Sort(matching)
	slices.Sort(nonMatching)
	pairs := float64(len(set.Pairs))
	t.Logf("model=%s dimensions=%d chunks=%d passages=%d", filepath.Base(dir), embedder.Info().Dimensions, len(uris), len(texts))
	t.Logf("corpus embed=%s per-query=%s", corpusEmbed, queryLatency)
	t.Logf("recall@1=%.2f recall@5=%.2f", float64(hitsAt1)/pairs, float64(hitsAt5)/pairs)
	t.Logf("matching     min=%.3f p10=%.3f p50=%.3f", matching[0], percentile(matching, 0.10), percentile(matching, 0.50))
	t.Logf("non-matching p50=%.3f p99=%.3f max=%.3f", percentile(nonMatching, 0.50), percentile(nonMatching, 0.99), nonMatching[len(nonMatching)-1])

	if hitsAt5 == 0 {
		t.Errorf("no sanity pair retrieved in the top 5 — the model or the sanity set is wrong")
	}
}

// TestCalibration_FusedRecall reports lexical-only, semantic-only and
// RRF-fused recall through a real HybridService. The pre-implementation
// benchmark modelled fusion outside the server and found it below semantic
// alone at ranks 3, 5 and 10; this is the first measurement of the shipped
// path. It is a measurement, not a gate, and it changes no fusion parameter
// whatever the rows say — that is a specification decision.
//
//	ACDC_MCP_TEST_MODEL_DIR=/tmp/potion-retrieval-32M \
//	  go test ./internal/embed/model2vec/ -run Calibration -v
func TestCalibration_FusedRecall(t *testing.T) {
	dir := os.Getenv("ACDC_MCP_TEST_MODEL_DIR")
	if dir == "" {
		t.Skip("set ACDC_MCP_TEST_MODEL_DIR to a potion model directory to calibrate")
	}
	quietLogging(t)
	set := loadSanitySet(t, envOr("ACDC_MCP_TEST_SANITY_SET", filepath.Join("testdata", "sanity-sample-content.json")))

	embedder, err := New(dir)
	if err != nil {
		t.Fatalf("New(%q): %v", dir, err)
	}

	passages := loadPassages(t, set.ContentDir)
	for _, pair := range set.Pairs {
		if _, ok := passages[pair.Chunk]; !ok {
			t.Fatalf("the sanity set names a chunk no corpus passage carries: %q", pair.Chunk)
		}
	}

	settings := calibrationSettings(dir)
	lexical := search.NewService(settings)
	hybrid, err := search.NewHybridService(lexical, embedder, settings)
	if err != nil {
		t.Fatalf("NewHybridService: %v", err)
	}
	defer hybrid.Close()
	indexChunks(t, hybrid, discoverChunks(t, set.ContentDir))

	// HybridService exposes no semantic-only path, by design: D11 makes the
	// semantic side unreachable on its own. The store's ranking is rebuilt
	// here so the middle row can be reported at all.
	semantic := newSemanticIndex(t, embedder, passages)

	lexicalHits := make([]int, len(recallDepths))
	semanticHits := make([]int, len(recallDepths))
	fusedHits := make([]int, len(recallDepths))
	for _, pair := range set.Pairs {
		lexicalResults, err := lexical.Search(pair.Query, candidateLimit)
		if err != nil {
			t.Fatalf("lexical Search(%q): %v", pair.Query, err)
		}
		fusedResults, err := hybrid.Search(pair.Query, candidateLimit)
		if err != nil {
			t.Fatalf("hybrid Search(%q): %v", pair.Query, err)
		}

		accumulate(lexicalHits, resultURIs(lexicalResults), pair.Chunk)
		accumulate(semanticHits, semantic.rank(t, embedder, pair.Query, settings.SemanticFloor, candidateLimit), pair.Chunk)
		accumulate(fusedHits, resultURIs(fusedResults), pair.Chunk)
	}

	t.Logf("model=%s chunks=%d pairs=%d per-side depth=%d floor=%.2f",
		filepath.Base(dir), len(passages), len(set.Pairs), candidateLimit, settings.SemanticFloor)
	logRecall(t, "lexical only", lexicalHits, len(set.Pairs))
	logRecall(t, "semantic only", semanticHits, len(set.Pairs))
	logRecall(t, "RRF fused", fusedHits, len(set.Pairs))

	if fusedHits[len(recallDepths)-1] == 0 {
		t.Errorf("the fused path retrieved no sanity pair at all — the harness, not the fusion, is wrong")
	}
}

// logRecall renders one row of the recall table.
func logRecall(t *testing.T, name string, hits []int, pairs int) {
	t.Helper()
	columns := make([]string, len(recallDepths))
	for i, depth := range recallDepths {
		columns[i] = fmt.Sprintf("recall@%d=%.2f", depth, float64(hits[i])/float64(pairs))
	}
	t.Logf("%-14s %s", name, strings.Join(columns, " "))
}

// accumulate credits every depth at which ranking retrieved want.
func accumulate(hits []int, ranking []string, want string) {
	for rank, uri := range ranking {
		if uri != want {
			continue
		}
		for i, depth := range recallDepths {
			if rank < depth {
				hits[i]++
			}
		}
		return
	}
}

func resultURIs(results []search.SearchResult) []string {
	uris := make([]string, len(results))
	for i, result := range results {
		uris[i] = result.ChunkURI
	}
	return uris
}

// semanticIndex is the max-pooled cosine ranking vector.Store computes,
// rebuilt over the same passages the server embeds.
type semanticIndex struct {
	uris    []string
	vectors [][]float32
	// owners maps each vector to the index in uris of the chunk it came from.
	owners []int
}

func newSemanticIndex(t *testing.T, embedder embed.Embedder, passages map[string][]string) *semanticIndex {
	t.Helper()

	uris := slices.Sorted(maps.Keys(passages))
	var texts []string
	var owners []int
	for i, uri := range uris {
		for _, text := range passages[uri] {
			texts = append(texts, text)
			owners = append(owners, i)
		}
	}
	vectors, err := embedder.EmbedDocuments(context.Background(), texts)
	if err != nil {
		t.Fatalf("EmbedDocuments: %v", err)
	}
	return &semanticIndex{uris: uris, vectors: vectors, owners: owners}
}

// rank returns the top k chunk URIs above floor, applying max-pooling and the
// floor exactly as vector.Store.Search and HybridService.vectorHits do
// together, and breaking ties on chunk URI as the store does on chunk ID.
func (s *semanticIndex) rank(t *testing.T, embedder embed.Embedder, query string, floor float64, k int) []string {
	t.Helper()

	queryVector, err := embedder.EmbedQuery(context.Background(), query)
	if err != nil {
		t.Fatalf("EmbedQuery(%q): %v", query, err)
	}

	best := make([]float64, len(s.uris))
	for i := range best {
		best[i] = math.Inf(-1)
	}
	for i, vector := range s.vectors {
		if score := dot(queryVector, vector); score > best[s.owners[i]] {
			best[s.owners[i]] = score
		}
	}

	order := make([]int, len(s.uris))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		if best[order[a]] != best[order[b]] {
			return best[order[a]] > best[order[b]]
		}
		return s.uris[order[a]] < s.uris[order[b]]
	})

	ranked := make([]string, 0, k)
	for _, i := range order {
		if len(ranked) == k || best[i] < floor {
			break
		}
		ranked = append(ranked, s.uris[i])
	}
	return ranked
}

// calibrationSettings mirror the shipped defaults, so the rows describe what
// an operator running --search-semantic-model actually gets.
func calibrationSettings(modelDir string) config.SearchSettings {
	return config.SearchSettings{
		MaxResults:    candidateLimit,
		InMemory:      true,
		KeywordsBoost: config.DefaultKeywordsBoost,
		HeadingBoost:  config.DefaultHeadingBoost,
		TitleBoost:    config.DefaultTitleBoost,
		PathBoost:     config.DefaultPathBoost,
		ContentBoost:  config.DefaultContentBoost,
		ResultMode:    config.SearchResultModeReferences,
		SemanticModel: modelDir,
		SemanticFloor: config.DefaultSemanticFloor,
	}
}

func indexChunks(t *testing.T, indexer search.Searcher, chunks []domain.Chunk) {
	t.Helper()

	stream := make(chan domain.Chunk, len(chunks))
	for _, chunk := range chunks {
		stream <- chunk
	}
	close(stream)
	if err := indexer.Index(context.Background(), stream); err != nil {
		t.Fatalf("Index: %v", err)
	}
}

// loadPassages maps every chunk URI in contentDir to the passage texts the
// server would hand the embedder for it. It is built on the server's own
// discovery and passage splitting: a second copy of the chunking rules would
// measure text the server never embeds.
func loadPassages(t *testing.T, contentDir string) map[string][]string {
	t.Helper()

	// MaxTokens zero is what a static model reports, so PrecisionWindow binds
	// here exactly as it does in the server. ID and Dimensions only salt the
	// passage cache key, which no measurement reads.
	model := vector.Model{ID: "calibration"}

	passages := make(map[string][]string)
	for _, chunk := range discoverChunks(t, contentDir) {
		for _, passage := range vector.BuildPassages(chunk, model) {
			passages[chunk.ChunkURI] = append(passages[chunk.ChunkURI], passage.Text)
		}
	}
	return passages
}

func discoverChunks(t *testing.T, contentDir string) []domain.Chunk {
	t.Helper()

	cp := content.NewContentProvider(contentDir)
	discovery, err := resources.Discover(context.Background(), cp, indexMetadata(t, cp), "acdc")
	if err != nil {
		t.Fatalf("Discover(%q): %v", contentDir, err)
	}
	if len(discovery.Chunks) == 0 {
		t.Fatalf("content dir %q discovered no chunks", contentDir)
	}
	return discovery.Chunks
}

// indexMetadata selects the discovery mode the server would select for this
// content root: the manifest's index section, the zero-config default when
// there is no manifest, or nil for a manifest that declares none, which routes
// to the legacy .acdc/resources scan.
func indexMetadata(t *testing.T, cp *content.ContentProvider) *domain.IndexMetadata {
	t.Helper()

	raw, err := os.ReadFile(cp.ConfigFile)
	if errors.Is(err, fs.ErrNotExist) {
		return domain.DefaultMetadata(filepath.Base(cp.ContentDir), "calibration").Index
	}
	if err != nil {
		t.Fatalf("read %q: %v", cp.ConfigFile, err)
	}
	var metadata domain.McpMetadata
	if err := yaml.Unmarshal(raw, &metadata); err != nil {
		t.Fatalf("parse %q: %v", cp.ConfigFile, err)
	}
	return metadata.Index
}

// loadSanitySet reads a sanity set. Its content_dir is taken as written:
// relative paths resolve against the package directory, which is where `go
// test` runs, so the in-repo set can name examples/sample-content and an
// external set can name an absolute corpus.
func loadSanitySet(t *testing.T, path string) sanitySet {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sanity set %q: %v", path, err)
	}
	var set sanitySet
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatalf("parse sanity set %q: %v", path, err)
	}
	if len(set.Pairs) == 0 {
		t.Fatalf("sanity set %q holds no pairs", path)
	}
	return set
}

// quietLogging discards the server's own log lines for the duration of a
// calibration run, so the only thing on the terminal is the measurement.
// Safe because nothing in this package runs in parallel — see
// TestNew_MakesNoNetworkRequest.
func quietLogging(t *testing.T) {
	t.Helper()

	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// dot is the cosine similarity of two unit-norm vectors.
func dot(a, b []float32) float64 {
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

// percentile is the nearest-rank percentile of an already sorted slice.
func percentile(sorted []float64, p float64) float64 {
	index := int(p * float64(len(sorted)))
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
