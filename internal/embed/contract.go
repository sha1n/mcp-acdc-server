package embed

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
)

// TestEmbedderContract asserts the obligations every Embedder owes its
// callers. Run it against the fake now and against each real adapter later:
// it is the artifact that makes swapping the backend safe.
//
// Run it under the race detector. The concurrency obligation is checked by
// overlapping callers, and without -race an unsynchronized adapter fails it
// only when the corruption happens to be observable.
//
// It takes testing.TB rather than *testing.T so a test can drive it through a
// recorder and assert that a broken implementation actually fails it.
func TestEmbedderContract(t testing.TB, newEmbedder func() Embedder) {
	t.Helper()

	ctx := context.Background()
	embedder := newEmbedder()
	info := embedder.Info()

	if info.Dimensions <= 0 {
		t.Fatalf("Info().Dimensions must be positive, got %d", info.Dimensions)
	}

	texts := fitCorpusToWindow([]string{"alpha", "a longer passage of ordinary prose", ""}, info.MaxTokens)

	documents, err := embedder.EmbedDocuments(ctx, texts)
	if err != nil {
		t.Fatalf("EmbedDocuments returned an error: %v", err)
	}
	if len(documents) != len(texts) {
		t.Fatalf("EmbedDocuments returned %d vectors for %d texts", len(documents), len(texts))
	}
	for i, vector := range documents {
		assertVectorContract(t, "EmbedDocuments", i, vector, info.Dimensions)
	}

	query, err := embedder.EmbedQuery(ctx, "alpha")
	if err != nil {
		t.Fatalf("EmbedQuery returned an error: %v", err)
	}
	assertVectorContract(t, "EmbedQuery", 0, query, info.Dimensions)

	assertRejectsOversizedInput(t, embedder, info)
	assertPreservesInputOrder(t, embedder, info)
	assertSafeForConcurrentUse(t, embedder, info)
}

// fitCorpusToWindow shortens the happy-path fixtures to at most maxTokens
// runes each so the width/norm/finiteness obligations can be asserted
// regardless of which window the embedder under test has. The suite cannot
// see the adapter's tokenizer, so runes are the same conservative proxy used
// to size the oversized-input case in assertRejectsOversizedInput, just
// applied in the safe direction: truncating by rune count never leaves more
// tokens than the window allows. A model with no window (maxTokens <= 0)
// leaves the fixtures untouched.
func fitCorpusToWindow(texts []string, maxTokens int) []string {
	if maxTokens <= 0 {
		return texts
	}
	fitted := make([]string, len(texts))
	for i, text := range texts {
		runes := []rune(text)
		if len(runes) > maxTokens {
			runes = runes[:maxTokens]
		}
		fitted[i] = string(runes)
	}
	return fitted
}

func assertVectorContract(t testing.TB, method string, index int, vector []float32, dimensions int) {
	t.Helper()

	if len(vector) != dimensions {
		t.Errorf("%s vector %d is %d wide, want %d", method, index, len(vector), dimensions)
		return
	}

	var sum float64
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			t.Errorf("%s vector %d contains a non-finite component", method, index)
			return
		}
		sum += float64(value) * float64(value)
	}
	if norm := math.Sqrt(sum); math.Abs(norm-1) > 1e-5 {
		t.Errorf("%s vector %d has norm %v, want 1", method, index, norm)
	}
}

// assertRejectsOversizedInput checks obligation 3 — that too-long input is
// reported, never silently truncated.
//
// The multiplier is deliberately generous: this suite cannot see the
// adapter's tokenizer, and the densest plausible tokenization is roughly one
// token per rune, so MaxTokens*8 runes exceeds any real window. A model with
// no window (MaxTokens <= 0) has no obligation to check.
func assertRejectsOversizedInput(t testing.TB, embedder Embedder, info ModelInfo) {
	t.Helper()

	if info.MaxTokens <= 0 {
		return
	}

	oversized := strings.Repeat("token ", info.MaxTokens*8)

	if _, err := embedder.EmbedDocuments(context.Background(), []string{oversized}); err == nil {
		t.Errorf("EmbedDocuments accepted an input longer than MaxTokens=%d", info.MaxTokens)
	} else {
		var tooLong *ErrInputTooLong
		if !errors.As(err, &tooLong) {
			t.Errorf("EmbedDocuments returned %v, want an *ErrInputTooLong", err)
		} else if tooLong.Index != 0 {
			t.Errorf("ErrInputTooLong.Index is %d, want 0", tooLong.Index)
		}
	}

	if _, err := embedder.EmbedQuery(context.Background(), oversized); err == nil {
		t.Errorf("EmbedQuery accepted an input longer than MaxTokens=%d", info.MaxTokens)
	}
}

// assertPreservesInputOrder checks that the vector at index i belongs to the
// text at index i.
//
// Order cannot be read off a single batch, so it is inferred from a second
// batch of the same texts rotated by one: every vector must be closest to the
// vector of its own text in the rotated batch. That assumes only that the
// embedder tells the fixtures apart, which any usable model does, and it is
// the obligation HybridService.embedBatch rests on when it attaches vectors[i]
// to the passage it queued at i.
func assertPreservesInputOrder(t testing.TB, embedder Embedder, info ModelInfo) {
	t.Helper()

	texts := fitCorpusToWindow([]string{
		"the treaty was signed in Vienna that spring",
		"quicksort partitions an array around a pivot",
		"chlorophyll absorbs light in the blue and red bands",
	}, info.MaxTokens)

	rotated := make([]string, len(texts))
	for i, text := range texts {
		rotated[rotatedPosition(i, len(texts))] = text
	}

	ctx := context.Background()
	vectors, err := embedder.EmbedDocuments(ctx, texts)
	if err != nil {
		t.Fatalf("EmbedDocuments returned an error: %v", err)
	}
	rotatedVectors, err := embedder.EmbedDocuments(ctx, rotated)
	if err != nil {
		t.Fatalf("EmbedDocuments returned an error: %v", err)
	}
	if len(vectors) != len(texts) || len(rotatedVectors) != len(rotated) {
		t.Fatalf("EmbedDocuments returned %d and %d vectors for %d texts", len(vectors), len(rotatedVectors), len(texts))
	}

	for i := range texts {
		want := rotatedPosition(i, len(texts))
		best, bestScore := -1, math.Inf(-1)
		for j, candidate := range rotatedVectors {
			if len(candidate) != len(vectors[i]) {
				t.Fatalf("EmbedDocuments returned vectors of differing widths, %d and %d", len(vectors[i]), len(candidate))
			}
			if score := dot(vectors[i], candidate); score > bestScore {
				best, bestScore = j, score
			}
		}
		if best != want {
			t.Errorf("vector %d matches the text embedded at %d, want %d: vectors must be returned in input order", i, best, want)
		}
	}
}

func rotatedPosition(index, length int) int {
	return (index + length - 1) % length
}

func dot(a, b []float32) float64 {
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

// contractConcurrency is how many callers overlap in the concurrency check.
// Both methods run from each of them because the documented promise is
// specifically that indexing and querying overlap.
const contractConcurrency = 8

// assertSafeForConcurrentUse overlaps EmbedDocuments and EmbedQuery callers.
//
// Results are gathered and asserted on the calling goroutine: testing.TB is
// not safe for concurrent use, and this suite is deliberately also run through
// a recorder that is plainer still.
func assertSafeForConcurrentUse(t testing.TB, embedder Embedder, info ModelInfo) {
	t.Helper()

	texts := fitCorpusToWindow([]string{"alpha", "a longer passage of ordinary prose"}, info.MaxTokens)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, contractConcurrency*2)
	vectors := make(chan []float32, contractConcurrency*(len(texts)+1))

	for range contractConcurrency {
		wg.Add(2)
		go func() {
			defer wg.Done()
			documents, err := embedder.EmbedDocuments(ctx, texts)
			if err != nil {
				errs <- err
				return
			}
			for _, vector := range documents {
				vectors <- vector
			}
		}()
		go func() {
			defer wg.Done()
			query, err := embedder.EmbedQuery(ctx, texts[0])
			if err != nil {
				errs <- err
				return
			}
			vectors <- query
		}()
	}
	wg.Wait()
	close(errs)
	close(vectors)

	for err := range errs {
		t.Errorf("concurrent call returned an error: %v: implementations must be safe for concurrent use", err)
	}
	index := 0
	for vector := range vectors {
		assertVectorContract(t, "concurrent call", index, vector, info.Dimensions)
		index++
	}
}
