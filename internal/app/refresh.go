package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/search"
)

// refreshInterval bounds how often a request may pay for a filesystem walk. It
// collapses a burst of calls into one revalidation while keeping the staleness
// window far below the latency of a person editing a document and then asking
// about it.
const refreshInterval = 2 * time.Second

// catalogRefresher implements mcp.Revalidator by re-walking the content root
// on a debounced schedule and republishing the catalog and search index only
// when something the walk found actually changed. Its filesystem and indexing
// collaborators are injected as function fields so tests can drive the
// debounce and change-detection logic without a real content root.
type catalogRefresher struct {
	mu sync.Mutex

	holder    *resources.CatalogHolder
	searcher  search.Searcher
	registrar *mcp.ResourceRegistrar

	fingerprint func(context.Context) (string, error)
	discover    func(context.Context) (resources.DiscoveryResult, error)
	assemble    func(resources.DiscoveryResult) (*resources.ResourceProvider, error)
	reindex     func(context.Context, *resources.ResourceProvider) error

	now      func() time.Time
	interval time.Duration

	lastCheck     time.Time
	walkDigest    string
	contentDigest string
}

var _ mcp.Revalidator = (*catalogRefresher)(nil)

// bindRegistrar binds the registrar built alongside this refresher.
// mcp.CreateServer needs a Revalidator to construct the ResourceRegistrar it
// returns, so registrar cannot be set at construction time; it is bound here,
// under mu like every other field Revalidate reads, once CreateServer
// returns it.
func (r *catalogRefresher) bindRegistrar(registrar *mcp.ResourceRegistrar) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registrar = registrar
}

// Revalidate re-walks the content root at most once per interval and
// republishes the catalog and search index only when the walk finds a
// genuine content change. It returns immediately while registrar is nil,
// which covers only the narrow window during server construction: mcp.
// CreateServer needs a Revalidator to build the registrar it returns, so the
// registrar cannot be bound to this refresher until after that call, and no
// request reaches the server before that binding completes.
func (r *catalogRefresher) Revalidate(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.registrar == nil {
		return
	}

	now := r.now()
	if !r.lastCheck.IsZero() && now.Sub(r.lastCheck) < r.interval {
		return
	}
	r.lastCheck = now

	walkDigest, err := r.fingerprint(ctx)
	if err != nil {
		slog.Warn("Catalog refresh failed to fingerprint content root, retrying next interval", "error", err)
		return
	}
	if walkDigest == r.walkDigest {
		// Idle path: the file set's paths, sizes and mtimes are unchanged, so
		// there is nothing to discover, index or republish.
		return
	}

	// A mid-session discovery, assembly or indexing failure must never take
	// down a live server: the previously published catalog and index stay in
	// place, the stored digests are left untouched, and the next interval's
	// walk retries from scratch.
	discovery, err := r.discover(ctx)
	if err != nil {
		slog.Warn("Catalog refresh discovery failed, retrying next interval", "error", err)
		return
	}

	newContentDigest := computeContentDigest(discovery.Sources)
	if newContentDigest == r.contentDigest {
		// Pure touch: the walk saw different paths, sizes or mtimes, but
		// every source's content fingerprint is unchanged, so there is
		// nothing to re-index or republish. The walk digest still advances
		// so an unchanged repeat of this same touch is quiet next time.
		r.walkDigest = walkDigest
		return
	}

	provider, err := r.assemble(discovery)
	if err != nil {
		slog.Warn("Catalog refresh failed to assemble resource provider, retrying next interval", "error", err)
		return
	}
	if err := r.reindex(ctx, provider); err != nil {
		slog.Warn("Catalog refresh failed to reindex content, retrying next interval", "error", err)
		return
	}

	// The index is replaced (inside reindex, above) before the catalog is
	// published here, and the catalog is published before the registrar is
	// synced against it. This ordering is not observable through search,
	// read or resource-read: every one of those handlers calls Revalidate
	// before touching the catalog or index, and Revalidate holds r.mu for
	// its entire body, so a concurrent caller blocks here until this swap
	// and sync have both completed rather than ever observing a call to
	// one collaborator and not the other. Only resources/list can lag: the
	// SDK serves it without a handler to hook, so it is the one surface
	// that is not gated by Revalidate and can reflect an older listing
	// until the next call that does trigger it.
	r.holder.Swap(provider)
	r.registrar.Sync(provider)

	r.walkDigest = walkDigest
	r.contentDigest = newContentDigest
	slog.Info("Catalog refresh republished catalog", "sources", len(discovery.Sources))
}

// computeContentDigest digests the selected source set's content fingerprints
// in discovery order: a SHA-256 over, per source, its URI, a NUL separator,
// its Fingerprint, and a newline. Unlike Fingerprint (which detects that the
// file set may have changed at all), this detects whether it actually did.
func computeContentDigest(sources []domain.SourceDocument) string {
	hasher := sha256.New()
	for _, source := range sources {
		// hash.Hash.Write never returns an error (per the io.Writer contract
		// it documents), so the write's result is deliberately discarded.
		_, _ = fmt.Fprintf(hasher, "%s\x00%s\n", source.URI, source.Fingerprint)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}
