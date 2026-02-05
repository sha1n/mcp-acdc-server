package resources

// Field name constants for resource metadata
const (
	FieldURI      = "uri"
	FieldName     = "name"
	FieldContent  = "content"
	FieldKeywords = "keywords"
)

// ResourceDefinition definition of an MCP resource
type ResourceDefinition struct {
	URI         string
	Name        string
	Description string
	MIMEType    string
	FilePath    string
	Keywords    []string // Optional keywords for search boosting
	Source      string   // Content location name
}
