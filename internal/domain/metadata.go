package domain

import (
	"fmt"
)

// ServerMetadata represents the server section of mcp-metadata.yaml
type ServerMetadata struct {
	Name         string `yaml:"name"`
	Version      string `yaml:"version"`
	Instructions string `yaml:"instructions"`
}

// ToolMetadata represents a tool definition in mcp-metadata.yaml
type ToolMetadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// IndexMetadata represents the index section of mcp-metadata.yaml.
type IndexMetadata struct {
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude,omitempty"`
}

// McpMetadata represents the root of mcp-metadata.yaml
type McpMetadata struct {
	Server ServerMetadata `yaml:"server"`
	Tools  []ToolMetadata `yaml:"tools"`
	Index  *IndexMetadata `yaml:"index,omitempty"`
}

// DefaultToolMetadata provides sensible defaults for known tools
var DefaultToolMetadata = map[string]ToolMetadata{
	"search": {
		Name: "search",
		Description: `Search across all indexed documentation using full-text search. This tool searches source titles, heading text, path segments, markdown content, and keywords to help you find relevant standards, guidelines, and documentation.

WHEN TO USE: Use this as your first step before generating code or reviewing implementations. Search for relevant topics to discover which resources apply to your task.

HOW IT WORKS: Searches are performed across source titles, heading paths, file/directory path segments, markdown content, and keywords. Results are chunk citations: each includes the source title, a chunk URI (open it with the read tool), a heading breadcrumb, the matched line range, and a relevant snippet. When the server is configured for content result mode, each result also includes the full text of the matched chunk.`,
	},
	"read": {
		Name: "read",
		Description: `Read the full content of a source document, or a single chunk within it. This tool allows you to retrieve markdown content using a URI returned by search or a resource listing.

WHEN TO USE: Use after you have found a relevant URI (e.g., via the search tool or by listing resources) and need to read its content to understand specific standards, guidelines, or instructions.

HOW IT WORKS: Provide a base URI (e.g., 'acdc://guides/getting-started') to read the whole document, or that same URI with a '#fragment' suffix (e.g., 'acdc://guides/getting-started#installation') to read a single chunk, exactly as returned by search. Resource URIs never include a file extension. The scheme defaults to 'acdc://' but may be configured differently by the server operator, so always use the scheme from the URI you were given.`,
	},
}

// DefaultIndexInclude selects the documentation layout assumed when no manifest is present.
var DefaultIndexInclude = []string{"README.md", "docs/**/*.md"}

// defaultInstructionsFormat is the server instructions used when no manifest is present.
// %s is substituted with the raw repository name.
const defaultInstructionsFormat = `Documentation for the %s repository: guides, references, design documents, specs and plans kept under docs/, plus the top-level README.

Search here before answering questions about this repository's conventions, architecture, decisions, or planned work. Prefer these documents over assumptions drawn from source code alone.`

// DefaultMetadata builds the metadata used when the content root has no mcp-metadata.yaml.
// repoName identifies the indexed repository in the server's display name and instructions.
func DefaultMetadata(repoName, version string) McpMetadata {
	return McpMetadata{
		Server: ServerMetadata{
			Name:         repoName + " Documentation",
			Version:      version,
			Instructions: fmt.Sprintf(defaultInstructionsFormat, repoName),
		},
		Index: &IndexMetadata{Include: append([]string(nil), DefaultIndexInclude...)},
	}
}

// GetToolMetadata returns metadata for the specified tool name, using overrides if provided
// in the config, otherwise falling back to defaults.
func (m *McpMetadata) GetToolMetadata(name string) ToolMetadata {
	for _, t := range m.Tools {
		if t.Name == name {
			return t
		}
	}
	return DefaultToolMetadata[name]
}

// ToolsMap returns tools as a map for easy lookup
func (m *McpMetadata) ToolsMap() (map[string]ToolMetadata, error) {
	tools := make(map[string]ToolMetadata)
	for _, t := range m.Tools {
		if _, exists := tools[t.Name]; exists {
			return nil, fmt.Errorf("duplicate tool name: %s", t.Name)
		}
		tools[t.Name] = t
	}
	return tools, nil
}

// Validate checks for required fields
func (m *McpMetadata) Validate() error {
	if m.Server.Name == "" {
		return fmt.Errorf("server name is required")
	}
	if m.Server.Version == "" {
		return fmt.Errorf("server version is required")
	}
	if m.Server.Instructions == "" {
		return fmt.Errorf("server instructions are required")
	}
	if m.Index != nil && len(m.Index.Include) == 0 {
		return fmt.Errorf("index.include requires at least one pattern")
	}

	for i, t := range m.Tools {
		if t.Name == "" {
			return fmt.Errorf("tool at index %d missing name", i)
		}
		if t.Description == "" {
			return fmt.Errorf("tool at index %d missing description", i)
		}
	}

	if _, err := m.ToolsMap(); err != nil {
		return err
	}

	return nil
}
