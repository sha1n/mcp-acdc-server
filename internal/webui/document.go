package webui

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

// ErrDocumentNotFound is returned by a DocumentReader for an unknown URI.
var ErrDocumentNotFound = errors.New("document not found")

// Document is a whole source document as served to MCP clients.
type Document struct {
	URI     string
	Title   string
	Path    string
	Content string
	Labels  []string
}

// DocumentReader resolves a search result's source URI to its document.
type DocumentReader interface {
	Document(uri string) (Document, error)
}

// HandlerOption customises NewHandler.
type HandlerOption func(*handler)

// WithDocumentReader enables /api/v1/document. Without it the route is 404.
func WithDocumentReader(reader DocumentReader) HandlerOption {
	return func(h *handler) { h.documents = reader }
}

type documentResponse struct {
	URI         string   `json:"uri"`
	Title       string   `json:"title"`
	Path        string   `json:"path"`
	Labels      []string `json:"labels"`
	Content     string   `json:"content"`
	ContentHTML string   `json:"content_html"`
}

func (h *handler) handleDocument(w http.ResponseWriter, r *http.Request) {
	uri := strings.TrimSpace(r.URL.Query().Get("uri"))
	if uri == "" {
		writeError(w, http.StatusBadRequest, "uri is required")
		return
	}

	doc, err := h.documents.Document(uri)
	if errors.Is(err, ErrDocumentNotFound) {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	if err != nil {
		slog.Error("web UI document read failed", "uri", uri, "error", err)
		writeError(w, http.StatusInternalServerError, "document read failed")
		return
	}

	writeJSON(w, http.StatusOK, documentResponse{
		URI:         doc.URI,
		Title:       doc.Title,
		Path:        doc.Path,
		Labels:      orEmpty(doc.Labels),
		Content:     doc.Content,
		ContentHTML: h.renderMarkdown(doc.Content),
	})
}
