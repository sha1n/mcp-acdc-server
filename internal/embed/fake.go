package embed

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sync"
)

// Fake is a deterministic Embedder for tests: the same text always yields the
// same vector. It is useful for mechanics and useless for ranking, because
// hash-derived vectors are near-orthogonal by construction. Tests that assert
// on rank inject explicit vectors instead.
//
// Fake counts tokens as runes, so a test can construct an input of an exact
// length to drive ErrInputTooLong.
type Fake struct {
	Dimensions int
	// MaxTokens of zero or less means unbounded, matching a static model.
	MaxTokens int

	mu        sync.Mutex
	documents int
}

// NewFake returns a Fake producing unit-norm vectors of the given width.
func NewFake(dimensions int) *Fake {
	return &Fake{Dimensions: dimensions}
}

func (f *Fake) Info() ModelInfo {
	return ModelInfo{Dimensions: f.Dimensions, MaxTokens: f.MaxTokens}
}

func (f *Fake) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		if f.tooLong(text) {
			return nil, &ErrInputTooLong{Index: i}
		}
		vectors[i] = f.vector(text)
	}
	f.mu.Lock()
	f.documents += len(texts)
	f.mu.Unlock()
	return vectors, nil
}

func (f *Fake) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.tooLong(text) {
		return nil, &ErrInputTooLong{Index: 0}
	}
	return f.vector(text), nil
}

// DocumentsEmbedded reports how many texts EmbedDocuments has embedded. It is
// the assertion the carry-forward tests rest on: a second identical index
// pass must not move it.
func (f *Fake) DocumentsEmbedded() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.documents
}

// ResetCounts zeroes the embed counter between phases of a test.
func (f *Fake) ResetCounts() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.documents = 0
}

func (f *Fake) tooLong(text string) bool {
	return f.MaxTokens > 0 && len([]rune(text)) > f.MaxTokens
}

// vector expands a SHA-256 of the text into Dimensions floats and normalizes,
// so the result is deterministic, text-sensitive and unit-norm.
func (f *Fake) vector(text string) []float32 {
	values := make([]float32, f.Dimensions)
	seed := sha256.Sum256([]byte(text))
	for i := range values {
		block := sha256.Sum256(append(seed[:], byte(i), byte(i>>8)))
		bits := binary.BigEndian.Uint32(block[:4])
		values[i] = float32(bits)/float32(math.MaxUint32)*2 - 1
	}

	var sum float64
	for _, v := range values {
		sum += float64(v) * float64(v)
	}
	norm := float32(math.Sqrt(sum))
	if norm == 0 {
		// Degenerate only if every component hashed to exactly zero; a fixed
		// basis vector keeps the unit-norm obligation true regardless.
		values[0] = 1
		return values
	}
	for i := range values {
		values[i] /= norm
	}
	return values
}
