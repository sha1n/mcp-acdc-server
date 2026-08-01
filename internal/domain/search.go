package domain

// Field name constants for indexed chunks.
const (
	FieldChunkID     = "chunk_id"
	FieldSourceID    = "source_id"
	FieldSourceURI   = "source_uri"
	FieldChunkURI    = "chunk_uri"
	FieldSourceTitle = "source_title"
	FieldSourcePath  = "source_path"
	FieldPathLabels  = "path_labels"
	FieldHeadingPath = "heading_path"
	FieldContent     = "content"
	FieldKeywords    = "keywords"
	FieldStartLine   = "start_line"
	FieldEndLine     = "end_line"
)

// Document is retained for the legacy resource stream. New search indexing uses Chunk.
type Document struct {
	URI      string   `json:"uri"`
	Name     string   `json:"name"`
	Content  string   `json:"content"`
	Keywords []string `json:"keywords,omitempty"`
}
