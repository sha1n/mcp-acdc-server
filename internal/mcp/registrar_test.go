package mcp

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/stretchr/testify/require"
)

// notificationSettleDelay must exceed the SDK's internal list-changed
// notification debounce so a test can tell "no notification was sent" apart
// from "the notification just hasn't arrived yet".
const notificationSettleDelay = 50 * time.Millisecond

// listCatalog is a resources.Catalog whose listing a test sets directly,
// independent of the content resources.NewResourceProvider would otherwise
// require.
type listCatalog struct {
	resourcesList []mcp.Resource
}

func (c listCatalog) ListResources() []mcp.Resource { return c.resourcesList }

func (c listCatalog) ReadResource(string) (string, error) { return "", nil }

func (c listCatalog) StreamChunks(_ context.Context, ch chan<- domain.Chunk) error {
	close(ch)
	return nil
}

func (c listCatalog) StreamResources(_ context.Context, ch chan<- domain.Document) error {
	close(ch)
	return nil
}

func newTestServer() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
}

// connectClient connects a client (and, if onResourceListChanged is non-nil,
// wires it to observe notifications/resources/list_changed) to s over an
// in-memory transport, returning the session and a cleanup func.
func connectClient(ctx context.Context, t *testing.T, s *mcp.Server, onResourceListChanged func()) *mcp.ClientSession {
	t.Helper()

	var handler func(context.Context, *mcp.ResourceListChangedRequest)
	if onResourceListChanged != nil {
		handler = func(context.Context, *mcp.ResourceListChangedRequest) { onResourceListChanged() }
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, &mcp.ClientOptions{
		ResourceListChangedHandler: handler,
	})

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	_, err := s.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	return session
}

func TestResourceRegistrar_Sync_IdenticalCatalog_NoNotification(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := newTestServer()
	catalog := listCatalog{resourcesList: []mcp.Resource{
		{URI: "acdc://a", Name: "A", Description: "doc a", MIMEType: "text/markdown"},
	}}

	registrar := NewResourceRegistrar(s, catalog, noopRevalidator{})
	registrar.Sync(catalog) // initial registration, before any session connects

	var notifications atomic.Int64
	connectClient(ctx, t, s, func() { notifications.Add(1) })

	// Resyncing the identical catalog must add and remove nothing, and so
	// must not trigger notifications/resources/list_changed.
	registrar.Sync(catalog)

	time.Sleep(notificationSettleDelay)
	require.Equal(t, int64(0), notifications.Load())
}

func TestResourceRegistrar_Sync_AddRemoveChange_ProducesExactSet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := newTestServer()
	initial := listCatalog{resourcesList: []mcp.Resource{
		{URI: "acdc://keep", Name: "Keep", Description: "unchanged", MIMEType: "text/markdown"},
		{URI: "acdc://remove", Name: "Remove", Description: "goes away", MIMEType: "text/markdown"},
		{URI: "acdc://change", Name: "Change", Description: "old description", MIMEType: "text/markdown"},
	}}

	registrar := NewResourceRegistrar(s, initial, noopRevalidator{})
	registrar.Sync(initial)

	updated := listCatalog{resourcesList: []mcp.Resource{
		{URI: "acdc://keep", Name: "Keep", Description: "unchanged", MIMEType: "text/markdown"},
		{URI: "acdc://change", Name: "Change", Description: "new description", MIMEType: "text/markdown"},
		{URI: "acdc://added", Name: "Added", Description: "brand new", MIMEType: "text/markdown"},
	}}
	registrar.Sync(updated)

	session := connectClient(ctx, t, s, nil)
	result, err := session.ListResources(ctx, nil)
	require.NoError(t, err)

	got := make(map[string]*mcp.Resource, len(result.Resources))
	for _, r := range result.Resources {
		got[r.URI] = r
	}

	require.Len(t, got, 3)
	require.NotContains(t, got, "acdc://remove")
	require.Contains(t, got, "acdc://keep")
	require.Contains(t, got, "acdc://added")
	require.Contains(t, got, "acdc://change")
	require.Equal(t, "new description", got["acdc://change"].Description)
}

// TestResourceRegistrar_Sync_UnparseableURI_SkippedWithoutPanic verifies that
// a resource whose URI fails url.Parse (an ASCII control character is legal
// in a relative path on Linux and macOS, but not in a URL) is skipped rather
// than reaching mcp.Server.AddResource, which panics on exactly this input,
// and that every other resource in the same Sync call still registers.
func TestResourceRegistrar_Sync_UnparseableURI_SkippedWithoutPanic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := newTestServer()
	catalog := listCatalog{resourcesList: []mcp.Resource{
		{URI: "acdc://good-before", Name: "Before", Description: "fine", MIMEType: "text/markdown"},
		{URI: "acdc://docs/a\nb.md", Name: "Bad", Description: "control character in URI", MIMEType: "text/markdown"},
		{URI: "acdc://good-after", Name: "After", Description: "fine", MIMEType: "text/markdown"},
	}}

	registrar := NewResourceRegistrar(s, catalog, noopRevalidator{})

	require.NotPanics(t, func() { registrar.Sync(catalog) })

	session := connectClient(ctx, t, s, nil)
	result, err := session.ListResources(ctx, nil)
	require.NoError(t, err)

	got := make(map[string]*mcp.Resource, len(result.Resources))
	for _, r := range result.Resources {
		got[r.URI] = r
	}
	require.Len(t, got, 2, "only the two parseable resources should be registered")
	require.Contains(t, got, "acdc://good-before")
	require.Contains(t, got, "acdc://good-after")
	require.NotContains(t, got, "acdc://docs/a\nb.md")
}

// TestResourceRegistrar_HandlersReadThroughConstructorHolder verifies that a
// registered resource handler resolves reads through the catalog passed to
// NewResourceRegistrar (which a caller may Swap later), never through the
// listing snapshot passed to a particular Sync call.
func TestResourceRegistrar_HandlersReadThroughConstructorHolder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := newTestServer()

	initialProvider, err := resources.NewResourceProvider([]resources.ResourceDefinition{
		{URI: "acdc://doc", Name: "Doc", Description: "d", MIMEType: "text/markdown", Content: "old content"},
	}, nil)
	require.NoError(t, err)
	holder := resources.NewCatalogHolder(initialProvider)

	registrar := NewResourceRegistrar(s, holder, noopRevalidator{})
	// Sync with a listing snapshot distinct from the holder, so a pass
	// proves reads resolve through the holder and not this value.
	registrar.Sync(listCatalog{resourcesList: holder.ListResources()})

	updatedProvider, err := resources.NewResourceProvider([]resources.ResourceDefinition{
		{URI: "acdc://doc", Name: "Doc", Description: "d", MIMEType: "text/markdown", Content: "new content"},
	}, nil)
	require.NoError(t, err)
	holder.Swap(updatedProvider)

	session := connectClient(ctx, t, s, nil)
	result, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "acdc://doc"})
	require.NoError(t, err)
	require.Len(t, result.Contents, 1)
	require.Equal(t, "new content", result.Contents[0].Text)
}
