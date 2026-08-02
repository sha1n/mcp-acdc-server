package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	mcpserver "github.com/sha1n/mcp-acdc-server/internal/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/stretchr/testify/require"
)

// fakeClock gives tests control over the instant catalogRefresher.now reports,
// without depending on wall-clock timing.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// noopTestRevalidator satisfies mcpserver.Revalidator without triggering any
// refresh, for wiring a ResourceRegistrar the tests do not intend to drive
// through its own Revalidate hook.
type noopTestRevalidator struct{}

func (noopTestRevalidator) Revalidate(context.Context) {}

// newTestRegistrar builds a real ResourceRegistrar backed by an unconnected
// in-process *mcp.Server: catalogRefresher.registrar is a concrete type, not
// an interface, so observing whether Sync ran requires a genuine registrar
// and, where a test needs to see the effect, a connected in-memory client. The
// backing server is returned alongside the registrar so a test can query it.
func newTestRegistrar(t *testing.T, catalog resources.Catalog) (*mcpserver.ResourceRegistrar, *mcpsdk.Server) {
	t.Helper()
	s := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	registrar := mcpserver.NewResourceRegistrar(s, catalog, noopTestRevalidator{})
	registrar.Sync(catalog)
	return registrar, s
}

// listResourceURIs connects a fresh in-memory client to the server backing
// registrar and returns the URIs currently registered, letting a test observe
// whether a ResourceRegistrar.Sync call actually changed the served listing.
// Both ends of the connection are closed before returning: the SDK's
// changeAndNotify branches on whether s has any live sessions, so a server
// session left open here would accumulate across calls within a test and
// skew that behavior for callers made afterwards.
func listResourceURIs(t *testing.T, s *mcpsdk.Server) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := s.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer func() { _ = serverSession.Close() }()
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	result, err := session.ListResources(ctx, nil)
	require.NoError(t, err)

	uris := make([]string, len(result.Resources))
	for i, r := range result.Resources {
		uris[i] = r.URI
	}
	return uris
}

func mustProvider(t *testing.T, sources []resources.ResourceDefinition) *resources.ResourceProvider {
	t.Helper()
	p, err := resources.NewResourceProvider(sources, nil)
	require.NoError(t, err)
	return p
}

// newTestRefresher builds a catalogRefresher with fakes that never fire
// unless a test overrides them, backed by a real holder/registrar pair seeded
// with initialProvider. The registrar's backing server is returned alongside
// it so a test can verify Sync's effect via listResourceURIs.
func newTestRefresher(t *testing.T, initialProvider *resources.ResourceProvider, clock *fakeClock) (*catalogRefresher, *mcpsdk.Server) {
	t.Helper()
	holder := resources.NewCatalogHolder(initialProvider)
	registrar, server := newTestRegistrar(t, holder)

	return &catalogRefresher{
		holder:    holder,
		registrar: registrar,
		fingerprint: func(context.Context) (string, error) {
			t.Fatal("fingerprint should not have been called")
			return "", nil
		},
		discover: func(context.Context) (resources.DiscoveryResult, error) {
			t.Fatal("discover should not have been called")
			return resources.DiscoveryResult{}, nil
		},
		assemble: func(resources.DiscoveryResult) (*resources.ResourceProvider, error) {
			t.Fatal("assemble should not have been called")
			return nil, nil
		},
		reindex: func(context.Context, *resources.ResourceProvider) error {
			t.Fatal("reindex should not have been called")
			return nil
		},
		now:      clock.Now,
		interval: refreshInterval,
	}, server
}

func TestCatalogRefresher_Revalidate_RegistrarNil_ReturnsImmediately(t *testing.T) {
	clock := newFakeClock()
	r := &catalogRefresher{
		now:      clock.Now,
		interval: refreshInterval,
		fingerprint: func(context.Context) (string, error) {
			t.Fatal("fingerprint should not have been called while registrar is nil")
			return "", nil
		},
	}

	r.Revalidate(context.Background())
	// No panic and no fingerprint call: the construction-window guard fired.
}

func TestCatalogRefresher_Revalidate_TwoCallsWithinInterval_FingerprintsOnce(t *testing.T) {
	clock := newFakeClock()
	initial := mustProvider(t, nil)
	r, _ := newTestRefresher(t, initial, clock)
	r.walkDigest = "digest-a" // matches the fingerprint below: the idle path, not discovery, follows

	var fingerprintCalls atomic.Int32
	r.fingerprint = func(context.Context) (string, error) {
		fingerprintCalls.Add(1)
		return "digest-a", nil
	}

	r.Revalidate(context.Background())
	r.Revalidate(context.Background())

	require.Equal(t, int32(1), fingerprintCalls.Load())
}

func TestCatalogRefresher_Revalidate_CallAfterInterval_FingerprintsAgain(t *testing.T) {
	clock := newFakeClock()
	initial := mustProvider(t, nil)
	r, _ := newTestRefresher(t, initial, clock)
	r.walkDigest = "digest-a" // matches the fingerprint below: the idle path, not discovery, follows

	var fingerprintCalls atomic.Int32
	r.fingerprint = func(context.Context) (string, error) {
		fingerprintCalls.Add(1)
		return "digest-a", nil
	}

	r.Revalidate(context.Background())
	clock.Advance(refreshInterval)
	r.Revalidate(context.Background())

	require.Equal(t, int32(2), fingerprintCalls.Load())
}

func TestCatalogRefresher_Revalidate_UnchangedWalkDigest_SkipsDiscovery(t *testing.T) {
	clock := newFakeClock()
	initial := mustProvider(t, nil)
	r, server := newTestRefresher(t, initial, clock)
	r.walkDigest = "digest-a"

	r.fingerprint = func(context.Context) (string, error) {
		return "digest-a", nil
	}
	// discover/assemble/reindex remain the t.Fatal fakes from newTestRefresher:
	// if the idle path is broken, this test fails loudly rather than silently
	// passing on an unexercised path.

	r.Revalidate(context.Background())

	require.Equal(t, "digest-a", r.walkDigest)
	require.Same(t, initial, r.holder.Current())
	require.Empty(t, listResourceURIs(t, server), "the idle path must not touch the registered listing")
}

func TestCatalogRefresher_Revalidate_ChangedWalkDigestEqualContentDigest_UpdatesWalkDigestOnly(t *testing.T) {
	clock := newFakeClock()
	initial := mustProvider(t, nil)
	r, server := newTestRefresher(t, initial, clock)
	r.walkDigest = "digest-a"

	sameSources := []resources.ResourceDefinition{
		{URI: "acdc://a", Fingerprint: "same-content"},
	}
	// Seed the content digest to match the source set discover will return,
	// so the touch is recognized as content-equal.
	seededContentDigest := computeContentDigest(sameSources)
	r.contentDigest = seededContentDigest

	var discoverCalls atomic.Int32
	r.fingerprint = func(context.Context) (string, error) {
		return "digest-b", nil // walk digest changed: a file was touched
	}
	r.discover = func(context.Context) (resources.DiscoveryResult, error) {
		discoverCalls.Add(1)
		return resources.DiscoveryResult{Sources: sameSources}, nil
	}
	// assemble/reindex remain the t.Fatal fakes: a pure touch must not reach
	// them.

	r.Revalidate(context.Background())

	require.Equal(t, int32(1), discoverCalls.Load())
	require.Equal(t, "digest-b", r.walkDigest, "walk digest must advance so a repeat of this touch is quiet")
	require.Equal(t, seededContentDigest, r.contentDigest, "content digest must not change on a pure touch")
	require.Same(t, initial, r.holder.Current(), "no swap on a pure touch")
	require.Empty(t, listResourceURIs(t, server), "a pure touch must not sync the registrar")
}

func TestCatalogRefresher_Revalidate_ChangedContent_AssemblesReindexesSwapsAndSyncs(t *testing.T) {
	clock := newFakeClock()
	initial := mustProvider(t, []resources.ResourceDefinition{
		{URI: "acdc://old", Name: "Old", MIMEType: "text/markdown", Content: "old"},
	})
	r, refresherServer := newTestRefresher(t, initial, clock)
	r.walkDigest = "digest-a"
	r.contentDigest = "content-a"

	discovery := resources.DiscoveryResult{
		Sources: []resources.ResourceDefinition{
			{URI: "acdc://new", Name: "New", MIMEType: "text/markdown", Content: "new", Fingerprint: "f2"},
		},
	}
	newProvider := mustProvider(t, discovery.Sources)

	var assembleCalls, reindexCalls atomic.Int32
	var reindexSawProvider *resources.ResourceProvider

	r.fingerprint = func(context.Context) (string, error) { return "digest-b", nil }
	r.discover = func(context.Context) (resources.DiscoveryResult, error) { return discovery, nil }
	r.assemble = func(got resources.DiscoveryResult) (*resources.ResourceProvider, error) {
		assembleCalls.Add(1)
		require.Equal(t, discovery, got)
		return newProvider, nil
	}
	r.reindex = func(_ context.Context, provider *resources.ResourceProvider) error {
		reindexCalls.Add(1)
		reindexSawProvider = provider
		// The holder must not be swapped yet: reindex runs before the swap,
		// not after. (Swap-before-Sync is not independently observable this
		// way since both happen after reindex returns.)
		require.Same(t, initial, r.holder.Current(), "reindex must run before the catalog is swapped")
		return nil
	}

	r.Revalidate(context.Background())

	require.Equal(t, int32(1), assembleCalls.Load())
	require.Equal(t, int32(1), reindexCalls.Load())
	require.Same(t, newProvider, reindexSawProvider)

	require.Same(t, newProvider, r.holder.Current(), "holder must be swapped to the assembled/reindexed provider")
	uris := listResourceURIs(t, refresherServer)
	require.ElementsMatch(t, []string{"acdc://new"}, uris, "registrar must be synced to the new listing")

	require.Equal(t, "digest-b", r.walkDigest)
	require.Equal(t, computeContentDigest(discovery.Sources), r.contentDigest)
}

func TestCatalogRefresher_Revalidate_FingerprintError_LeavesSnapshotAndDigestsUntouched(t *testing.T) {
	clock := newFakeClock()
	initial := mustProvider(t, nil)
	r, server := newTestRefresher(t, initial, clock)
	r.walkDigest = "digest-a"
	r.contentDigest = "content-a"

	r.fingerprint = func(context.Context) (string, error) {
		return "", errors.New("fingerprint boom")
	}

	r.Revalidate(context.Background())

	require.Equal(t, "digest-a", r.walkDigest)
	require.Equal(t, "content-a", r.contentDigest)
	require.Same(t, initial, r.holder.Current())
	require.Empty(t, listResourceURIs(t, server), "a fingerprint failure must not sync the registrar")
}

func TestCatalogRefresher_Revalidate_DiscoverError_LeavesSnapshotAndDigestsUntouched(t *testing.T) {
	clock := newFakeClock()
	initial := mustProvider(t, nil)
	r, server := newTestRefresher(t, initial, clock)
	r.walkDigest = "digest-a"
	r.contentDigest = "content-a"

	r.fingerprint = func(context.Context) (string, error) { return "digest-b", nil }
	r.discover = func(context.Context) (resources.DiscoveryResult, error) {
		return resources.DiscoveryResult{}, errors.New("discover boom")
	}

	r.Revalidate(context.Background())

	require.Equal(t, "digest-a", r.walkDigest)
	require.Equal(t, "content-a", r.contentDigest)
	require.Same(t, initial, r.holder.Current())
	require.Empty(t, listResourceURIs(t, server), "a discovery failure must not sync the registrar")
}

func TestCatalogRefresher_Revalidate_AssembleError_LeavesSnapshotAndDigestsUntouched(t *testing.T) {
	clock := newFakeClock()
	initial := mustProvider(t, nil)
	r, server := newTestRefresher(t, initial, clock)
	r.walkDigest = "digest-a"
	r.contentDigest = "content-a"

	discovery := resources.DiscoveryResult{
		Sources: []resources.ResourceDefinition{{URI: "acdc://a", Fingerprint: "f2"}},
	}
	r.fingerprint = func(context.Context) (string, error) { return "digest-b", nil }
	r.discover = func(context.Context) (resources.DiscoveryResult, error) { return discovery, nil }
	r.assemble = func(resources.DiscoveryResult) (*resources.ResourceProvider, error) {
		return nil, errors.New("assemble boom")
	}

	r.Revalidate(context.Background())

	require.Equal(t, "digest-a", r.walkDigest)
	require.Equal(t, "content-a", r.contentDigest)
	require.Same(t, initial, r.holder.Current())
	require.Empty(t, listResourceURIs(t, server), "an assemble failure must not sync the registrar")
}

func TestCatalogRefresher_Revalidate_ReindexError_LeavesSnapshotAndDigestsUntouched(t *testing.T) {
	clock := newFakeClock()
	initial := mustProvider(t, nil)
	r, server := newTestRefresher(t, initial, clock)
	r.walkDigest = "digest-a"
	r.contentDigest = "content-a"

	discovery := resources.DiscoveryResult{
		Sources: []resources.ResourceDefinition{{URI: "acdc://a", Fingerprint: "f2"}},
	}
	assembled := mustProvider(t, discovery.Sources)
	r.fingerprint = func(context.Context) (string, error) { return "digest-b", nil }
	r.discover = func(context.Context) (resources.DiscoveryResult, error) { return discovery, nil }
	r.assemble = func(resources.DiscoveryResult) (*resources.ResourceProvider, error) { return assembled, nil }
	r.reindex = func(context.Context, *resources.ResourceProvider) error {
		return errors.New("reindex boom")
	}

	r.Revalidate(context.Background())

	require.Equal(t, "digest-a", r.walkDigest)
	require.Equal(t, "content-a", r.contentDigest)
	require.Same(t, initial, r.holder.Current(), "the previously published snapshot must survive a reindex failure")
	require.Empty(t, listResourceURIs(t, server), "a reindex failure must not sync the registrar")
}

// TestCatalogRefresher_Revalidate_PersistentFingerprintError_DebouncesAcrossFailures
// pins lastCheck's placement: it is set once per interval regardless of
// whether fingerprint succeeds, so a persistently failing fingerprint still
// walks at most once per interval rather than re-walking on every call.
func TestCatalogRefresher_Revalidate_PersistentFingerprintError_DebouncesAcrossFailures(t *testing.T) {
	clock := newFakeClock()
	initial := mustProvider(t, nil)
	r, _ := newTestRefresher(t, initial, clock)

	var fingerprintCalls atomic.Int32
	r.fingerprint = func(context.Context) (string, error) {
		fingerprintCalls.Add(1)
		return "", errors.New("fingerprint boom")
	}

	r.Revalidate(context.Background())
	r.Revalidate(context.Background())

	require.Equal(t, int32(1), fingerprintCalls.Load())
}

// TestCatalogRefresher_Revalidate_ConcurrentCalls_SingleChangeProducesOneRebuild
// exercises the mutex under -race and confirms the debounce collapses a burst
// of concurrent callers reacting to one content change into exactly one
// rebuild.
func TestCatalogRefresher_Revalidate_ConcurrentCalls_SingleChangeProducesOneRebuild(t *testing.T) {
	clock := newFakeClock()
	initial := mustProvider(t, nil)
	r, _ := newTestRefresher(t, initial, clock)
	r.walkDigest = "digest-a"
	r.contentDigest = "content-a"

	discovery := resources.DiscoveryResult{
		Sources: []resources.ResourceDefinition{{URI: "acdc://a", Fingerprint: "f2"}},
	}
	assembled := mustProvider(t, discovery.Sources)

	var fingerprintCalls, discoverCalls, assembleCalls, reindexCalls atomic.Int32
	r.fingerprint = func(context.Context) (string, error) {
		fingerprintCalls.Add(1)
		return "digest-b", nil
	}
	r.discover = func(context.Context) (resources.DiscoveryResult, error) {
		discoverCalls.Add(1)
		return discovery, nil
	}
	r.assemble = func(resources.DiscoveryResult) (*resources.ResourceProvider, error) {
		assembleCalls.Add(1)
		return assembled, nil
	}
	r.reindex = func(context.Context, *resources.ResourceProvider) error {
		reindexCalls.Add(1)
		return nil
	}

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			r.Revalidate(context.Background())
		}()
	}
	wg.Wait()

	// The fake clock never advances and the debounce check runs inside the
	// same critical section as the rest of the algorithm, so exactly one of
	// the twenty concurrent callers gets past step 1; the rest see a
	// just-set lastCheck and return immediately. A leaked debounce (e.g. the
	// interval check reading state outside the lock) would let more than one
	// goroutine reach these collaborators.
	require.Equal(t, int32(1), fingerprintCalls.Load())
	require.Equal(t, int32(1), discoverCalls.Load())
	require.Equal(t, int32(1), assembleCalls.Load())
	require.Equal(t, int32(1), reindexCalls.Load())
	require.Same(t, assembled, r.holder.Current())
}
