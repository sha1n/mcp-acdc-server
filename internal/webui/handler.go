// Package webui serves the embedded web search UI and its JSON API.
package webui

import (
	"net/http"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/search"
)

// Backend is the search capability the web UI depends on.
type Backend interface {
	SearchLabeled(query string, labels []string, limit int) ([]search.SearchResult, error)
	Labels(prefix string, limit int) ([]search.LabelCount, error)
}

// NewHandler is a stub pending implementation.
func NewHandler(backend Backend, settings config.UISettings) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
	})
}
