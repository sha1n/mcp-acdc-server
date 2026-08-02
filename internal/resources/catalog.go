package resources

import (
	"context"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
)

// Catalog is the read side of a discovered source and chunk catalog: the
// operations the MCP server and the indexer perform against one snapshot.
type Catalog interface {
	ListResources() []mcp.Resource
	ReadResource(uri string) (string, error)
	StreamChunks(ctx context.Context, ch chan<- domain.Chunk) error
	StreamResources(ctx context.Context, ch chan<- domain.Document) error
}

var (
	_ Catalog = (*ResourceProvider)(nil)
	_ Catalog = (*CatalogHolder)(nil)
)

// CatalogHolder publishes one immutable *ResourceProvider snapshot at a time.
// Readers resolve the current snapshot once per call, so a Swap that
// publishes a new catalog never tears a call already in flight: it either
// runs entirely against the snapshot it started with, or entirely against
// the one that replaces it.
type CatalogHolder struct {
	current atomic.Pointer[ResourceProvider]
}

// NewCatalogHolder creates a holder publishing provider as its initial
// snapshot. provider must not be nil.
func NewCatalogHolder(provider *ResourceProvider) *CatalogHolder {
	holder := &CatalogHolder{}
	holder.current.Store(provider)
	return holder
}

// Current returns the snapshot most recently published by Swap, or the one
// passed to NewCatalogHolder if Swap has not been called yet.
func (h *CatalogHolder) Current() *ResourceProvider {
	return h.current.Load()
}

// Swap publishes provider as the new current snapshot. provider must not be
// nil.
func (h *CatalogHolder) Swap(provider *ResourceProvider) {
	h.current.Store(provider)
}

// ListResources lists resources from the current snapshot.
func (h *CatalogHolder) ListResources() []mcp.Resource {
	return h.current.Load().ListResources()
}

// ReadResource reads a resource from the current snapshot.
func (h *CatalogHolder) ReadResource(uri string) (string, error) {
	return h.current.Load().ReadResource(uri)
}

// StreamChunks streams chunks from the snapshot current at the time of the
// call; a later Swap does not affect an in-flight stream.
func (h *CatalogHolder) StreamChunks(ctx context.Context, ch chan<- domain.Chunk) error {
	return h.current.Load().StreamChunks(ctx, ch)
}

// StreamResources streams resources from the snapshot current at the time of
// the call; a later Swap does not affect an in-flight stream.
func (h *CatalogHolder) StreamResources(ctx context.Context, ch chan<- domain.Document) error {
	return h.current.Load().StreamResources(ctx, ch)
}
