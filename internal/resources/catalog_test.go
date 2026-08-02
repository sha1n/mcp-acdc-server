package resources

import (
	"context"
	"sync"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/stretchr/testify/require"
)

func newTestProvider(t *testing.T, prefix string, n int) *ResourceProvider {
	t.Helper()
	sources := make([]domain.SourceDocument, 0, n)
	chunks := make([]domain.Chunk, 0, n)
	for i := 0; i < n; i++ {
		uri := prefix + string(rune('a'+i))
		sources = append(sources, domain.SourceDocument{ID: uri, URI: uri, Name: uri, Content: uri + "-content"})
		chunkURI := uri + "#chunk"
		chunks = append(chunks, domain.Chunk{ID: chunkURI, SourceID: uri, SourceURI: uri, ChunkURI: chunkURI, Content: uri + "-chunk"})
	}
	provider, err := NewResourceProvider(sources, chunks)
	require.NoError(t, err)
	return provider
}

func TestCatalogHolder_ReadsResolveAgainstTheCurrentSnapshot(t *testing.T) {
	providerA := newTestProvider(t, "acdc://a-", 2)
	providerB := newTestProvider(t, "acdc://b-", 2)

	holder := NewCatalogHolder(providerA)

	resourcesA := holder.ListResources()
	require.Len(t, resourcesA, 2)
	for _, r := range resourcesA {
		require.Contains(t, r.URI, "acdc://a-")
		body, err := holder.ReadResource(r.URI)
		require.NoError(t, err)
		require.Contains(t, body, "-content")
	}
	_, err := holder.ReadResource("acdc://b-a")
	require.ErrorIs(t, err, ErrUnknownResource)

	holder.Swap(providerB)

	resourcesB := holder.ListResources()
	require.Len(t, resourcesB, 2)
	for _, r := range resourcesB {
		require.Contains(t, r.URI, "acdc://b-")
		body, err := holder.ReadResource(r.URI)
		require.NoError(t, err)
		require.Contains(t, body, "-content")
	}
	_, err = holder.ReadResource("acdc://a-a")
	require.ErrorIs(t, err, ErrUnknownResource)
}

// TestCatalogHolder_SwapUnderConcurrentReaders races readers against a writer
// swapping between two providers with disjoint URI sets. It exercises the
// property under test with -race: every ListResources call must see URIs
// from exactly one provider; a pinned snapshot's ReadResource for a URI its
// own ListResources returned must always succeed; and holder.ReadResource
// itself, called directly while Swap is in flight, must never resolve two
// different snapshots within one call.
func TestCatalogHolder_SwapUnderConcurrentReaders(t *testing.T) {
	const iterations = 500
	const readers = 8

	providerA := newTestProvider(t, "acdc://a-", 3)
	providerB := newTestProvider(t, "acdc://b-", 3)
	holder := NewCatalogHolder(providerA)

	var wg sync.WaitGroup
	wg.Add(readers + 1)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				holder.Swap(providerB)
			} else {
				holder.Swap(providerA)
			}
		}
	}()

	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				// A single ListResources call must never combine URIs from both
				// providers: it resolves one snapshot and builds the whole list
				// from it, regardless of swaps landing mid-loop in other goroutines.
				list := holder.ListResources()
				require.NotEmpty(t, list)
				prefix := list[0].URI[:len("acdc://a-")]
				require.Contains(t, []string{"acdc://a-", "acdc://b-"}, prefix)
				for _, res := range list {
					require.Truef(t, len(res.URI) >= len(prefix) && res.URI[:len(prefix)] == prefix,
						"listing mixed URIs from both providers: %v", list)
				}

				// Pin one snapshot via Current so the listing and the read that
				// follows cannot straddle an intervening Swap: holder.ListResources
				// followed by holder.ReadResource as two independent calls would be
				// racing the writer goroutine even with a correct implementation,
				// since the two providers' URI sets are disjoint by design.
				snapshot := holder.Current()
				pinnedList := snapshot.ListResources()
				require.NotEmpty(t, pinnedList)
				body, err := snapshot.ReadResource(pinnedList[0].URI)
				require.NoError(t, err)
				require.Contains(t, body, "-content")

				// Also call the holder's own ReadResource delegate directly under
				// concurrent Swap pressure. Racing it against a stale URI has two
				// legitimate outcomes: the provider that owned the URI is still
				// current (exact content match), or a Swap has since replaced it
				// with the other, disjoint provider (ErrUnknownResource). Anything
				// else — a different error, or a success with mismatched content —
				// would mean the call resolved two different snapshots internally.
				uri := list[0].URI
				readBody, readErr := holder.ReadResource(uri)
				if readErr != nil {
					require.ErrorIsf(t, readErr, ErrUnknownResource, "unexpected error reading %s", uri)
				} else {
					require.Equalf(t, uri+"-content", readBody, "content mismatch reading %s: got %q", uri, readBody)
				}
			}
		}()
	}

	wg.Wait()
}

// TestCatalogHolder_StreamChunksUsesOneSnapshot verifies StreamChunks resolves
// its snapshot once at the start of the call: swapping the holder mid-stream
// must not change which provider's chunks are delivered.
func TestCatalogHolder_StreamChunksUsesOneSnapshot(t *testing.T) {
	providerA := newTestProvider(t, "acdc://a-", 5)
	providerB := newTestProvider(t, "acdc://b-", 5)
	holder := NewCatalogHolder(providerA)

	// Small buffer plus a pausing reader forces StreamChunks to block
	// mid-flight so the swap below lands while the call is in progress.
	stream := make(chan domain.Chunk, 1)
	errs := make(chan error, 1)
	go func() {
		errs <- holder.StreamChunks(context.Background(), stream)
	}()

	first := <-stream
	require.Contains(t, first.ChunkURI, "acdc://a-")

	holder.Swap(providerB)

	received := []domain.Chunk{first}
	for len(received) < 5 {
		received = append(received, <-stream)
	}
	require.NoError(t, <-errs)

	require.Len(t, received, 5)
	for _, chunk := range received {
		require.Contains(t, chunk.ChunkURI, "acdc://a-")
	}
}

// TestCatalogHolder_StreamResourcesUsesOneSnapshot verifies StreamResources
// delegates to the current snapshot's own StreamResources rather than being
// left unimplemented or bypassed, and that it resolves that snapshot once at
// the start of the call: swapping the holder mid-stream must not change
// which provider's resources are delivered.
func TestCatalogHolder_StreamResourcesUsesOneSnapshot(t *testing.T) {
	providerA := newTestProvider(t, "acdc://a-", 5)
	providerB := newTestProvider(t, "acdc://b-", 5)
	holder := NewCatalogHolder(providerA)

	// Small buffer plus a pausing reader forces StreamResources to block
	// mid-flight so the swap below lands while the call is in progress.
	stream := make(chan domain.Document, 1)
	errs := make(chan error, 1)
	go func() {
		errs <- holder.StreamResources(context.Background(), stream)
	}()

	first := <-stream
	require.Contains(t, first.URI, "acdc://a-")

	holder.Swap(providerB)

	received := []domain.Document{first}
	for len(received) < 5 {
		received = append(received, <-stream)
	}
	require.NoError(t, <-errs)

	require.Len(t, received, 5)
	for _, document := range received {
		require.Contains(t, document.URI, "acdc://a-")
	}
}
