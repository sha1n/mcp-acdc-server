// Package webui serves the read-only browser UI and its JSON API.
package webui

import (
	"context"
	"errors"
	"net/http"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/sha1n/mcp-acdc-server/internal/webui/render"
)

// SearchBackend is the search capability the web UI depends on.
type SearchBackend interface {
	LabelledSearch(ctx context.Context, query string, labels []string, limit int) ([]search.LabelledResult, error)
	Labels(ctx context.Context, prefix string, limit int) ([]search.LabelCount, error)
}

// ErrDocumentNotFound reports an unknown or non-source document URI.
var ErrDocumentNotFound = errors.New("document not found")

// Document is a source document as served to MCP clients.
type Document struct {
	URI     string
	Title   string
	Path    string
	Labels  []string
	Content string
	// Source is the authored body before the cross-reference transform.
	Source string
}

// DocumentSnapshot is a consistent view of one catalog generation.
type DocumentSnapshot interface {
	Document(uri string) (Document, error)
	render.LinkResolver
}

// DocumentReader yields catalog snapshots.
type DocumentReader interface {
	Snapshot(ctx context.Context) DocumentSnapshot
}

// Mount registers the web UI pages under /ui and the JSON API under /api/v1/ on mux.
// It panics when backend or documents is nil, as both are wiring defects.
func Mount(mux *http.ServeMux, backend SearchBackend, documents DocumentReader, settings config.UISettings) {
	if backend == nil {
		panic("webui: nil search backend")
	}
	if documents == nil {
		panic("webui: nil document reader")
	}
	pages := &pageHandler{}
	mux.HandleFunc("/ui", pages.redirectToRoot)
	mux.Handle("/ui/", pages)
	mux.Handle("/api/v1/", &apiHandler{backend: backend, documents: documents, settings: settings})
}
