// Package vector holds the semantic side of retrieval: it splits chunks into
// embeddable passages and scans their vectors.
package vector

import (
	"fmt"
	"sort"
)

// Hit is one chunk's best similarity to a query.
type Hit struct {
	ChunkID string
	Score   float64
}

// Store holds passage vectors in one flat slice with a parallel slice of
// owning chunk IDs, so adding a vector allocates nothing per vector and a
// search is a single linear pass.
//
// A Store is built once and then read; it carries no lock of its own, and its
// owner publishes a completed Store by swapping the pointer.
type Store struct {
	dimensions int
	data       []float32
	chunkIDs   []string
}

// NewStore returns an empty Store for vectors of the given width.
func NewStore(dimensions int) *Store {
	return &Store{dimensions: dimensions}
}

// Dimensions is the vector width this Store accepts.
func (s *Store) Dimensions() int { return s.dimensions }

// Len is the number of vectors held, which exceeds the number of chunks
// whenever a chunk was split into several passages.
func (s *Store) Len() int { return len(s.chunkIDs) }

// Add appends one passage vector owned by chunkID.
func (s *Store) Add(chunkID string, vector []float32) error {
	if len(vector) != s.dimensions {
		return fmt.Errorf("vector for chunk %q is %d wide, want %d", chunkID, len(vector), s.dimensions)
	}
	s.data = append(s.data, vector...)
	s.chunkIDs = append(s.chunkIDs, chunkID)
	return nil
}

// Search returns the k chunks most similar to query, max-pooled: a chunk
// scores as its single best passage, never an average, so a query matching
// one paragraph deep inside a long chunk still retrieves it.
//
// Every vector is unit-norm by the Embedder contract, so the dot product is
// the cosine similarity and no division is needed. Ties break on chunk ID so
// the order is stable for the golden harness.
func (s *Store) Search(query []float32, k int) []Hit {
	if k <= 0 || len(query) != s.dimensions || len(s.chunkIDs) == 0 {
		return nil
	}

	best := make(map[string]float64, len(s.chunkIDs))
	for i, chunkID := range s.chunkIDs {
		offset := i * s.dimensions
		var score float64
		for d, value := range query {
			score += float64(s.data[offset+d]) * float64(value)
		}
		if current, seen := best[chunkID]; !seen || score > current {
			best[chunkID] = score
		}
	}

	hits := make([]Hit, 0, len(best))
	for chunkID, score := range best {
		hits = append(hits, Hit{ChunkID: chunkID, Score: score})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ChunkID < hits[j].ChunkID
	})

	if len(hits) > k {
		hits = hits[:k]
	}
	return hits
}
