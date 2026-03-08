package domain

// Field name constants for indexed documents
const (
	FieldURI      = "uri"
	FieldName     = "name"
	FieldContent  = "content"
	FieldKeywords = "keywords"
)

// Document represents a document to index
type Document struct {
	URI      string   `json:"uri"`
	Name     string   `json:"name"`
	Content  string   `json:"content"`
	Keywords []string `json:"keywords,omitempty"`
}
