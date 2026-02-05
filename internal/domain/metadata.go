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

// ContentLocation represents a content source location in the config file
type ContentLocation struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Path        string `yaml:"path"`
	Type        string `yaml:"type,omitempty"` // Optional adapter type (e.g., "acdc-mcp", "legacy")
}

// McpMetadata represents the root of mcp-metadata.yaml
type McpMetadata struct {
	Server  ServerMetadata    `yaml:"server"`
	Tools   []ToolMetadata    `yaml:"tools"`
	Content []ContentLocation `yaml:"content"`
}

// DefaultToolMetadata provides sensible defaults for known tools
var DefaultToolMetadata = map[string]ToolMetadata{
	"search": {
		Name: "search",
		Description: `Search across all development resources using full-text search. This tool searches resource names, descriptions, and content to help you find relevant standards, guidelines, and documentation.

WHEN TO USE: Use this as your first step before generating code or reviewing implementations. Search for relevant topics to discover which resources apply to your task.

HOW IT WORKS: Searches are performed across resource names, descriptions, and full markdown content. Results include the resource name, URI, and a relevant text snippet showing where your query was found.`,
	},
	"read": {
		Name: "read",
		Description: `Read the full content of a specified resource. This tool allows you to retrieve the complete markdown content of any development resource using its URI.

WHEN TO USE: Use after you have found a relevant resource URI (e.g., via the search tool or by listing resources) and need to read its full content to understand specific standards, guidelines, or instructions.

HOW IT WORKS: Provide the URI of the resource you wish to read (e.g., 'acdc://guides/getting-started.md'). The tool returns the full markdown content of the resource with frontmatter removed.`,
	},
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

// ValidateContentLocations validates a slice of content locations
func ValidateContentLocations(locations []ContentLocation) error {
	if len(locations) == 0 {
		return fmt.Errorf("at least one content location is required")
	}

	names := make(map[string]bool)
	for i, loc := range locations {
		if loc.Name == "" {
			return fmt.Errorf("content location at index %d: name is required", i)
		}
		if loc.Description == "" {
			return fmt.Errorf("content location at index %d: description is required", i)
		}
		if loc.Path == "" {
			return fmt.Errorf("content location at index %d: path is required", i)
		}
		if names[loc.Name] {
			return fmt.Errorf("content location at index %d: duplicate name %q", i, loc.Name)
		}
		names[loc.Name] = true
	}

	return nil
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

	if err := ValidateContentLocations(m.Content); err != nil {
		return err
	}

	return nil
}
