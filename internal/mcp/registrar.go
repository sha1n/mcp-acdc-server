package mcp

import (
	"log/slog"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
)

// ResourceRegistrar keeps a server's registered resources reconciled with the
// catalog snapshot it is given, so notifications/resources/list_changed fires
// on a genuine change to the resource set and not on every refresh.
//
// It is not safe for concurrent use; its caller serializes Sync calls.
type ResourceRegistrar struct {
	server      *mcp.Server
	catalog     resources.Catalog
	revalidator Revalidator
	registered  map[string]mcp.Resource
}

// NewResourceRegistrar creates a registrar that registers resources on s and
// dispatches their reads through catalog. catalog is retained and handed to
// every resource handler Sync registers, so a later Swap of a *resources.
// CatalogHolder passed here is observed by those handlers even though they
// were registered against an earlier snapshot.
//
// revalidator must not be nil: it is invoked directly from the resource
// handlers Sync registers, with no nil check on that path. mcp.CreateServer
// is the only caller with a legitimate reason to pass one in, and it
// normalizes nil to a no-op Revalidator before reaching here; any other
// caller must supply a real Revalidator (or a no-op of its own) up front.
func NewResourceRegistrar(s *mcp.Server, catalog resources.Catalog, revalidator Revalidator) *ResourceRegistrar {
	return &ResourceRegistrar{
		server:      s,
		catalog:     catalog,
		revalidator: revalidator,
		registered:  make(map[string]mcp.Resource),
	}
}

// Sync reconciles the server's registered resources with catalog.
//
// A resource whose URI fails url.Parse is skipped rather than registered:
// mcp.Server.AddResource panics on exactly that input, and an ASCII control
// character — illegal in a URL but legal in a relative file path on Linux and
// macOS — is enough to trigger it. The skipped URI is left out of the next
// registered set entirely so it is never mistaken for something the server
// already holds; a later Sync call keeps re-validating and re-warning on it
// rather than silently treating it as registered or attempting to remove a
// URI the server was never given.
func (r *ResourceRegistrar) Sync(catalog resources.Catalog) {
	listing := catalog.ListResources()

	next := make(map[string]mcp.Resource, len(listing))
	for _, res := range listing {
		if _, err := url.Parse(res.URI); err != nil {
			slog.Warn("Skipping resource with unparseable URI", "uri", res.URI, "error", err)
			continue
		}
		next[res.URI] = res
	}

	var removedURIs []string
	for uri := range r.registered {
		if _, ok := next[uri]; !ok {
			removedURIs = append(removedURIs, uri)
		}
	}
	if len(removedURIs) > 0 {
		r.server.RemoveResources(removedURIs...)
	}

	for _, res := range listing {
		if _, valid := next[res.URI]; !valid {
			continue // unparseable URI, already logged and excluded above
		}
		if prev, existed := r.registered[res.URI]; existed && resourceMetadataEqual(prev, res) {
			continue
		}
		r.server.AddResource(&res, makeResourceHandler(r.catalog, r.revalidator, res.URI))
	}

	r.registered = next
}

// resourceMetadataEqual reports whether the fields the MCP resources/list
// response exposes to clients are identical. mcp.Resource is not reliably
// comparable with ==, since it may grow uncomparable fields.
func resourceMetadataEqual(a, b mcp.Resource) bool {
	return a.Name == b.Name && a.Description == b.Description && a.MIMEType == b.MIMEType
}
