package webui

import "errors"

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
