// Measurement instrument, not a regression gate.
//
// Nothing here asserts a timing or a size, and `go test` never runs it —
// benchmarks execute only under -bench, which `make bench` supplies. The
// figures it reports are inputs to a decision, so each one travels with its
// own provenance: the corpus is named by flag and its chunk count is
// reported alongside every measurement.
//
// The corpus is built through the production chunking path rather than
// hand-assembled, because term-dictionary size and posting-list length are
// what a query fanning out across many segments actually costs. A synthetic
// corpus of one repeated string measures segment counts correctly and query
// latency not at all.
//
// The default corpus is small enough that every batch size collapses into a
// single batch, so a meaningful run names a real corpus:
//
//	go test ./internal/search/ -run '^$' -bench=. -corpus=$HOME/docs -corpus-dup=2
//
// The package path must come before -corpus and -corpus-dup. Those flags are
// registered by the test binary, not by `go test`, so `go test -corpus-dup=2
// ./internal/search/` fails with a misleading "no Go files in ." — `go test`
// consumes the unknown flag itself and never sees the package path.
package search

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/scorch"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/stretchr/testify/require"
)

var (
	benchCorpusDir = flag.String("corpus", filepath.Join("testdata", "corpus"),
		"directory of Markdown files the benchmarks index")
	benchCorpusDup = flag.Int("corpus-dup", 1,
		"how many times to repeat the corpus, with distinct identifiers, to reach a target scale")
)

type benchIndexKind struct {
	name string
	open func(tb testing.TB) (bleve.Index, func())
}

func benchIndexKinds() []benchIndexKind {
	return []benchIndexKind{
		{
			name: "memory",
			open: func(tb testing.TB) (bleve.Index, func()) {
				tb.Helper()
				index, err := newMemoryIndex(buildMapping())
				require.NoError(tb, err)
				return index, func() {}
			},
		},
		{
			name: "disk",
			open: func(tb testing.TB) (bleve.Index, func()) {
				tb.Helper()
				dir := filepath.Join(tb.TempDir(), "index")
				index, err := newDiskIndex(dir, buildMapping())
				require.NoError(tb, err)
				return index, func() { _ = os.RemoveAll(dir) }
			},
		},
	}
}

// benchBatchSizes spans a deliberately segment-heavy point, the shipped
// default, a bucket an order of magnitude above it, and one batch for the
// whole corpus — the only way to reach a single root segment in memory. The
// segment-heavy point is fixed at 100 rather than derived from
// defaultBatchSize: it exists to reproduce the many-segment comparison point
// that motivated the shipped default in the first place, which only stays a
// distinct measurement if it does not move in lockstep with that default.
// dedupeInts tolerates the two coinciding, e.g. if the default is ever 100
// again.
func benchBatchSizes(corpusSize int) []int {
	sizes := dedupeInts([]int{100, defaultBatchSize, 10000})
	kept := sizes[:0]
	for _, size := range sizes {
		if size < corpusSize {
			kept = append(kept, size)
		}
	}
	return append(kept, corpusSize)
}

// dedupeInts returns the sorted, duplicate-free contents of sizes.
func dedupeInts(sizes []int) []int {
	sort.Ints(sizes)
	out := sizes[:0]
	for i, size := range sizes {
		if i == 0 || size != sizes[i-1] {
			out = append(out, size)
		}
	}
	return out
}

// benchCorpus loads every Markdown file under dir through ParseMarkdown ->
// BuildSourceDocument -> ChunkMarkdown, so path_labels, keywords, heading
// paths and content are populated exactly as they are at runtime. A file the
// production path would reject is skipped, as discovery skips it under the
// lenient policy.
func benchCorpus(tb testing.TB, dir string, dup int) []domain.Chunk {
	tb.Helper()

	var (
		chunks []domain.Chunk
		files  int
	)
	walkErr := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		parsed, err := content.ParseMarkdown(raw, content.FrontmatterOptional)
		if err != nil {
			return nil
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		source, err := content.BuildSourceDocument(parsed, content.SourceOptions{
			URI:          "acdc://bench/" + relative,
			FilePath:     path,
			RelativePath: relative,
			Raw:          raw,
		})
		if err != nil {
			return nil
		}
		fileChunks, err := content.ChunkMarkdown(source, parsed)
		if err != nil {
			return nil
		}
		files++
		chunks = append(chunks, fileChunks...)
		return nil
	})
	require.NoError(tb, walkErr)
	require.NotEmpty(tb, chunks, "no Markdown chunks under %s", dir)
	tb.Logf("corpus %s: %d files, %d chunks, dup %d", dir, files, len(chunks), dup)

	if dup <= 1 {
		return chunks
	}
	scaled := make([]domain.Chunk, 0, len(chunks)*dup)
	for copyIndex := 0; copyIndex < dup; copyIndex++ {
		for _, chunk := range chunks {
			if copyIndex > 0 {
				suffix := fmt.Sprintf("~%d", copyIndex)
				chunk.ID += suffix
				chunk.SourceID += suffix
				chunk.SourceURI += suffix
				chunk.ChunkURI += suffix
			}
			scaled = append(scaled, chunk)
		}
	}
	tb.Logf("corpus scaled to %d chunks", len(scaled))
	return scaled
}

func chunkStream(chunks []domain.Chunk) <-chan domain.Chunk {
	stream := make(chan domain.Chunk, len(chunks))
	for _, chunk := range chunks {
		stream <- chunk
	}
	close(stream)
	return stream
}

// rootSegments counts the segments a query must fan out across.
//
// It counts the live root snapshot rather than reading scorch's
// num_root_memorysegments stat: introduceSegment stores that stat from the
// previous root before appending the batch's own segment, and only the
// merger and persister loops refresh it afterwards. Neither loop runs when
// the index has no path, so on an in-memory index the stat is permanently
// one short.
func rootSegments(tb testing.TB, index bleve.Index) float64 {
	tb.Helper()

	internal, err := index.Advanced()
	require.NoError(tb, err)
	scorchIndex, ok := internal.(*scorch.Scorch)
	require.True(tb, ok, "expected a scorch index")

	reader, err := scorchIndex.Reader()
	require.NoError(tb, err)
	defer func() { require.NoError(tb, reader.Close()) }()

	snapshot, ok := reader.(*scorch.IndexSnapshot)
	require.True(tb, ok, "expected a scorch index snapshot")
	return float64(len(snapshot.Segments()))
}

// settledRootSegments waits for the root snapshot to stop moving, then counts
// it.
//
// batchIndex returns as soon as the last batch is introduced, but on disk
// scorch's persister and merger keep working afterwards, so both the segment
// count and the process heap read at that moment describe a pipeline
// mid-flight and vary run to run on identical input. Polling until the count
// holds still reports the index a caller would actually query.
//
// An index with no path skips the poll entirely: scorch gates both background
// loops on a non-empty path, so nothing can move the root snapshot once the
// last batch is in, and a wait would only cost the in-memory measurements
// their turnaround.
//
// The wait is bounded: a pipeline that never settles yields a logged, moving
// figure rather than a benchmark that hangs.
//
// Settling makes the figure describe a quiescent index; it does not make the
// resting count identical run to run. At batch sizes small enough to introduce
// hundreds of segments, how many of them the on-disk persister groups into
// each file segment depends on when it happens to wake, so the count it comes
// to rest at still moves by a few between runs.
func settledRootSegments(tb testing.TB, index bleve.Index) float64 {
	tb.Helper()

	if index.Name() == "" {
		return rootSegments(tb, index)
	}

	const (
		pollInterval  = 100 * time.Millisecond
		stableSamples = 5
		maxWait       = 30 * time.Second
	)

	deadline := time.Now().Add(maxWait)
	segments := rootSegments(tb, index)
	for stable := 1; stable < stableSamples; {
		if time.Now().After(deadline) {
			tb.Logf("root segments still moving after %s; reporting %.0f", maxWait, segments)
			break
		}
		time.Sleep(pollInterval)
		current := rootSegments(tb, index)
		if current == segments {
			stable++
			continue
		}
		segments, stable = current, 1
	}
	return segments
}

// heapAllocMiB reports live Go heap after a collection. Callers subtract two
// readings to size what was built between them; see the heapMiB metric for
// what that difference does and does not mean.
func heapAllocMiB() float64 {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return float64(stats.HeapAlloc) / (1 << 20)
}

func BenchmarkIndexBuild(b *testing.B) {
	corpus := benchCorpus(b, *benchCorpusDir, *benchCorpusDup)
	for _, kind := range benchIndexKinds() {
		for _, batchSize := range benchBatchSizes(len(corpus)) {
			b.Run(fmt.Sprintf("%s/batch=%d", kind.name, batchSize), func(b *testing.B) {
				var segments, heapMiB float64
				for b.Loop() {
					b.StopTimer()
					index, cleanup := kind.open(b)
					stream := chunkStream(corpus)
					baseline := heapAllocMiB()
					b.StartTimer()

					require.NoError(b, batchIndex(context.Background(), index, stream, batchSize))

					b.StopTimer()
					segments = settledRootSegments(b, index)
					// heapMiB is a magnitude indicator, not the index's heap.
					// The baseline is taken once the corpus and the channel
					// buffered from it already exist, and that buffer has
					// drained into garbage by the time this reading collects,
					// so the difference is the index's heap less the buffer's:
					// an understatement, and one that grows with the corpus. An
					// on-disk index keeps its segments in mmap rather than on
					// the Go heap and can retain less than the buffer released,
					// which is why its figure can come out below zero.
					heapMiB = heapAllocMiB() - baseline
					require.NoError(b, index.Close())
					cleanup()
					b.StartTimer()
				}
				b.ReportMetric(float64(len(corpus)), "chunks")
				b.ReportMetric(segments, "segments")
				b.ReportMetric(heapMiB, "heapMiB")
			})
		}
	}
}

// benchQueries are answered against whichever corpus is configured. They are
// ordinary documentation phrasings rather than terms tuned to one corpus, so
// the same set stays meaningful when -corpus changes.
var benchQueries = []string{
	"authentication",
	"how do i configure the search index",
	"token rotation",
	"content directory layout",
}

func BenchmarkQuery(b *testing.B) {
	corpus := benchCorpus(b, *benchCorpusDir, *benchCorpusDup)
	for _, kind := range benchIndexKinds() {
		for _, batchSize := range benchBatchSizes(len(corpus)) {
			b.Run(fmt.Sprintf("%s/batch=%d", kind.name, batchSize), func(b *testing.B) {
				index, cleanup := kind.open(b)
				b.Cleanup(func() {
					_ = index.Close()
					cleanup()
				})
				require.NoError(b, batchIndex(context.Background(), index, chunkStream(corpus), batchSize))

				// Set the field directly rather than calling Index: the index is
				// already built, and Service.Close would then close it twice.
				service := NewService(testSettings())
				service.index = index

				// A query that matches nothing is far cheaper than one that
				// retrieves, and would quietly bias the whole measurement
				// towards the empty-result path. Run each query once before the
				// timed loop and fail on an empty result: this asserts that the
				// measurement is meaningful, never how fast or how large it is.
				for _, benchQuery := range benchQueries {
					results, err := service.Search(benchQuery, 0)
					require.NoError(b, err)
					require.NotEmpty(b, results,
						"query %q retrieved nothing from corpus %s; it measures the empty-result path, not query latency",
						benchQuery, *benchCorpusDir)
					b.Logf("query %q: %d results", benchQuery, len(results))
				}

				// Settle before timing so every iteration queries the same
				// index rather than one the persister and merger are still
				// reshaping.
				b.Logf("segments before the timed loop: %.0f", settledRootSegments(b, index))

				query := 0
				for b.Loop() {
					_, err := service.Search(benchQueries[query%len(benchQueries)], 0)
					require.NoError(b, err)
					query++
				}
				b.StopTimer()

				b.ReportMetric(float64(len(corpus)), "chunks")
				b.ReportMetric(settledRootSegments(b, index), "segments")
			})
		}
	}
}
