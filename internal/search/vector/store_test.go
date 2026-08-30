package vector

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStore_SearchRanksByCosineSimilarity(t *testing.T) {
	store := NewStore(2)
	require.NoError(t, store.Add("north", []float32{0, 1}))
	require.NoError(t, store.Add("east", []float32{1, 0}))
	require.NoError(t, store.Add("northeast", []float32{0.7071068, 0.7071068}))

	hits := store.Search([]float32{1, 0}, 3)

	require.Len(t, hits, 3)
	require.Equal(t, "east", hits[0].ChunkID)
	require.Equal(t, "northeast", hits[1].ChunkID)
	require.Equal(t, "north", hits[2].ChunkID)
	require.InDelta(t, 1.0, hits[0].Score, 1e-6)
	require.InDelta(t, 0.7071068, hits[1].Score, 1e-6)
	require.InDelta(t, 0.0, hits[2].Score, 1e-6)
}

// Max-pooling is the point of multi-vector chunks: a query matching one
// paragraph deep inside a long chunk must rank that chunk on that paragraph,
// not on an average diluted by the rest of it.
func TestStore_MaxPoolsMultipleVectorsPerChunk(t *testing.T) {
	store := NewStore(2)
	require.NoError(t, store.Add("long", []float32{0, 1}))
	require.NoError(t, store.Add("long", []float32{1, 0}))
	require.NoError(t, store.Add("short", []float32{0.7071068, 0.7071068}))

	hits := store.Search([]float32{1, 0}, 10)

	require.Len(t, hits, 2, "a chunk appears once however many vectors it owns")
	require.Equal(t, "long", hits[0].ChunkID)
	require.InDelta(t, 1.0, hits[0].Score, 1e-6)
	require.Equal(t, "short", hits[1].ChunkID)
}

func TestStore_SearchTruncatesToK(t *testing.T) {
	store := NewStore(2)
	require.NoError(t, store.Add("a", []float32{1, 0}))
	require.NoError(t, store.Add("b", []float32{0.9, 0.4358899}))
	require.NoError(t, store.Add("c", []float32{0, 1}))

	require.Len(t, store.Search([]float32{1, 0}, 2), 2)
	require.Len(t, store.Search([]float32{1, 0}, 0), 0)
	require.Len(t, store.Search([]float32{1, 0}, -1), 0)
}

func TestStore_SearchBreaksTiesOnChunkID(t *testing.T) {
	store := NewStore(2)
	require.NoError(t, store.Add("zebra", []float32{1, 0}))
	require.NoError(t, store.Add("apple", []float32{1, 0}))
	require.NoError(t, store.Add("mango", []float32{1, 0}))

	hits := store.Search([]float32{1, 0}, 3)

	require.Equal(t, []string{"apple", "mango", "zebra"}, []string{hits[0].ChunkID, hits[1].ChunkID, hits[2].ChunkID})
}

func TestStore_RejectsWrongWidthVectors(t *testing.T) {
	store := NewStore(3)

	require.Error(t, store.Add("a", []float32{1, 0}))
	require.Equal(t, 0, store.Len())
	require.Equal(t, 3, store.Dimensions())
}

func TestStore_EmptyStoreAndMismatchedQueryReturnNothing(t *testing.T) {
	store := NewStore(2)
	require.Empty(t, store.Search([]float32{1, 0}, 5))

	require.NoError(t, store.Add("a", []float32{1, 0}))
	require.Empty(t, store.Search([]float32{1, 0, 0}, 5), "a query of the wrong width matches nothing")
}

func TestStore_LenCountsVectorsNotChunks(t *testing.T) {
	store := NewStore(2)
	require.NoError(t, store.Add("a", []float32{1, 0}))
	require.NoError(t, store.Add("a", []float32{0, 1}))

	require.Equal(t, 2, store.Len())
}

func TestIsZero_DistinguishesNoEmbeddingFromASmallOne(t *testing.T) {
	require.True(t, IsZero([]float32{0, 0, 0}))
	require.True(t, IsZero(nil))
	require.False(t, IsZero([]float32{0, 1e-30, 0}))
	require.False(t, IsZero([]float32{-1, 0, 0}))
}
