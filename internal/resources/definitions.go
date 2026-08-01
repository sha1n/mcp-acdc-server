package resources

import "github.com/sha1n/mcp-acdc-server/internal/domain"

// Field name constants for resource metadata
const (
	FieldURI      = "uri"
	FieldName     = "name"
	FieldContent  = "content"
	FieldKeywords = "keywords"
)

// ResourceDefinition is retained as the legacy name for a source document.
type ResourceDefinition = domain.SourceDocument
