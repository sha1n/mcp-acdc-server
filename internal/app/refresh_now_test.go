package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/stretchr/testify/require"
)

func TestCatalogRefresher_RevalidateNow_WithinInterval_RepublishesChangedContent(t *testing.T) {
	clock := newFakeClock()
	initial := mustProvider(t, nil)
	r, server := newTestRefresher(t, initial, clock)
	r.walkDigest = "digest-a"
	r.contentDigest = "content-a"

	discovery := resources.DiscoveryResult{
		Sources: []resources.ResourceDefinition{
			{URI: "acdc://new", Name: "New", MIMEType: "text/markdown", Content: "new", Fingerprint: "f2"},
		},
	}
	newProvider := mustProvider(t, discovery.Sources)
	walkDigest := "digest-a"
	r.fingerprint = func(context.Context) (string, error) { return walkDigest, nil }
	r.discover = func(context.Context) (resources.DiscoveryResult, error) { return discovery, nil }
	r.assemble = func(resources.DiscoveryResult) (*resources.ResourceProvider, error) { return newProvider, nil }
	r.reindex = func(context.Context, *resources.ResourceProvider) error { return nil }

	r.Revalidate(context.Background())
	walkDigest = "digest-b"
	r.revalidateNow(context.Background())

	require.Same(t, newProvider, r.holder.Current())
	require.ElementsMatch(t, []string{"acdc://new"}, listResourceURIs(t, server))
	require.Equal(t, "digest-b", r.walkDigest)
	require.Equal(t, computeContentDigest(discovery.Sources), r.contentDigest)
}

func TestCatalogRefresher_RevalidateNow_LeavesTheRequestDebounceWindowUnchanged(t *testing.T) {
	clock := newFakeClock()
	r, _ := newTestRefresher(t, mustProvider(t, nil), clock)
	r.walkDigest = "digest-a"
	var fingerprintCalls atomic.Int32
	r.fingerprint = func(context.Context) (string, error) {
		fingerprintCalls.Add(1)
		return "digest-a", nil
	}

	r.revalidateNow(context.Background())
	r.Revalidate(context.Background())
	require.Equal(t, int32(2), fingerprintCalls.Load(), "a request right after a forced check is not debounced by it")

	clock.Advance(refreshInterval / 2)
	r.revalidateNow(context.Background())
	clock.Advance(refreshInterval / 2)
	r.Revalidate(context.Background())
	require.Equal(t, int32(4), fingerprintCalls.Load(), "the window still runs from the last request-triggered check")
}

func TestCatalogRefresher_RevalidateNow_FailureKeepsTheSnapshotAndDigests(t *testing.T) {
	clock := newFakeClock()
	initial := mustProvider(t, nil)
	r, server := newTestRefresher(t, initial, clock)
	r.walkDigest = "digest-a"
	r.contentDigest = "content-a"
	r.fingerprint = func(context.Context) (string, error) { return "digest-b", nil }
	r.discover = func(context.Context) (resources.DiscoveryResult, error) {
		return resources.DiscoveryResult{}, errors.New("discover boom")
	}

	r.revalidateNow(context.Background())

	require.Same(t, initial, r.holder.Current())
	require.Empty(t, listResourceURIs(t, server))
	require.Equal(t, "digest-a", r.walkDigest)
	require.Equal(t, "content-a", r.contentDigest)
}

func TestCatalogRefresher_RevalidateNow_RegistrarNil_ReturnsImmediately(t *testing.T) {
	r := &catalogRefresher{
		fingerprint: func(context.Context) (string, error) {
			t.Fatal("fingerprint should not have been called while registrar is nil")
			return "", nil
		},
	}

	r.revalidateNow(context.Background())
}
