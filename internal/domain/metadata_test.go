package domain

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// validContent returns a valid content location for use in tests
func validContent() []ContentLocation {
	return []ContentLocation{{Name: "docs", Description: "Documentation", Path: "/path/to/docs"}}
}

func TestMcpMetadata_Validate(t *testing.T) {
	tests := []struct {
		name    string
		meta    McpMetadata
		wantErr bool
	}{
		{
			name: "Valid",
			meta: McpMetadata{
				Server:  ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
				Tools:   []ToolMetadata{{Name: "t", Description: "d"}},
				Content: validContent(),
			},
			wantErr: false,
		},
		{
			name: "Missing Server Name",
			meta: McpMetadata{
				Server:  ServerMetadata{Name: "", Version: "1", Instructions: "i"},
				Content: validContent(),
			},
			wantErr: true,
		},
		{
			name: "Missing Server Version",
			meta: McpMetadata{
				Server:  ServerMetadata{Name: "s", Version: "", Instructions: "i"},
				Content: validContent(),
			},
			wantErr: true,
		},
		{
			name: "Missing Instructions",
			meta: McpMetadata{
				Server:  ServerMetadata{Name: "s", Version: "1", Instructions: ""},
				Content: validContent(),
			},
			wantErr: true,
		},
		{
			name: "Missing Tool Name",
			meta: McpMetadata{
				Server:  ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
				Tools:   []ToolMetadata{{Name: "", Description: "d"}},
				Content: validContent(),
			},
			wantErr: true,
		},
		{
			name: "Missing Tool Description",
			meta: McpMetadata{
				Server:  ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
				Tools:   []ToolMetadata{{Name: "t", Description: ""}},
				Content: validContent(),
			},
			wantErr: true,
		},
		{
			name: "Duplicate Tool Name",
			meta: McpMetadata{
				Server:  ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
				Tools:   []ToolMetadata{{Name: "t", Description: "d"}, {Name: "t", Description: "d2"}},
				Content: validContent(),
			},
			wantErr: true,
		},
		{
			name: "Valid with no tools",
			meta: McpMetadata{
				Server:  ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
				Content: validContent(),
			},
			wantErr: false,
		},
		{
			name: "Missing Content",
			meta: McpMetadata{
				Server: ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.meta.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("McpMetadata.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetToolMetadata(t *testing.T) {
	meta := McpMetadata{
		Server: ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
		Tools: []ToolMetadata{
			{Name: "search", Description: "custom search"},
			{Name: "other", Description: "other tool"},
		},
	}

	t.Run("Override First", func(t *testing.T) {
		got := meta.GetToolMetadata("search")
		if got.Description != "custom search" {
			t.Errorf("expected custom search, got %s", got.Description)
		}
	})

	t.Run("Override Second", func(t *testing.T) {
		got := meta.GetToolMetadata("other")
		if got.Description != "other tool" {
			t.Errorf("expected other tool, got %s", got.Description)
		}
	})

	t.Run("Default (After loop)", func(t *testing.T) {
		got := meta.GetToolMetadata("read")
		expected := DefaultToolMetadata["read"].Description
		if got.Description != expected {
			t.Errorf("expected default read description, got %s", got.Description)
		}
	})

	t.Run("Unknown (After loop)", func(t *testing.T) {
		got := meta.GetToolMetadata("unknown")
		if got.Name != "" || got.Description != "" {
			t.Errorf("expected empty metadata for unknown tool, got %+v", got)
		}
	})

	t.Run("Empty Tools", func(t *testing.T) {
		emptyMeta := McpMetadata{}
		got := emptyMeta.GetToolMetadata("search")
		if got.Description != DefaultToolMetadata["search"].Description {
			t.Errorf("expected default search even with empty tools")
		}
	})
}

func TestToolsMap(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		meta := McpMetadata{
			Tools: []ToolMetadata{
				{Name: "t1", Description: "d1"},
				{Name: "t2", Description: "d2"},
			},
		}
		m, err := meta.ToolsMap()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(m) != 2 {
			t.Errorf("expected 2 tools, got %d", len(m))
		}
	})

	t.Run("Empty", func(t *testing.T) {
		meta := McpMetadata{}
		m, err := meta.ToolsMap()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(m) != 0 {
			t.Errorf("expected 0 tools, got %d", len(m))
		}
	})

	t.Run("Duplicate", func(t *testing.T) {
		meta := McpMetadata{
			Tools: []ToolMetadata{
				{Name: "t", Description: "d1"},
				{Name: "t", Description: "d2"},
			},
		}
		_, err := meta.ToolsMap()
		if err == nil {
			t.Fatal("expected error for duplicate tool name")
		}
	})
}

func TestValidateContentLocations(t *testing.T) {
	tests := []struct {
		name        string
		locations   []ContentLocation
		wantErr     bool
		errContains string
	}{
		{
			name: "Valid single location",
			locations: []ContentLocation{
				{Name: "docs", Description: "Documentation", Path: "/path/to/docs"},
			},
			wantErr: false,
		},
		{
			name: "Valid multiple locations",
			locations: []ContentLocation{
				{Name: "docs", Description: "Documentation", Path: "/path/to/docs"},
				{Name: "internal", Description: "Internal guides", Path: "/path/to/internal"},
			},
			wantErr: false,
		},
		{
			name:        "Empty locations",
			locations:   []ContentLocation{},
			wantErr:     true,
			errContains: "at least one content location is required",
		},
		{
			name:        "Nil locations",
			locations:   nil,
			wantErr:     true,
			errContains: "at least one content location is required",
		},
		{
			name: "Missing name",
			locations: []ContentLocation{
				{Name: "", Description: "Documentation", Path: "/path/to/docs"},
			},
			wantErr:     true,
			errContains: "name is required",
		},
		{
			name: "Missing description",
			locations: []ContentLocation{
				{Name: "docs", Description: "", Path: "/path/to/docs"},
			},
			wantErr:     true,
			errContains: "description is required",
		},
		{
			name: "Missing path",
			locations: []ContentLocation{
				{Name: "docs", Description: "Documentation", Path: ""},
			},
			wantErr:     true,
			errContains: "path is required",
		},
		{
			name: "Duplicate names",
			locations: []ContentLocation{
				{Name: "docs", Description: "Documentation", Path: "/path/to/docs"},
				{Name: "docs", Description: "Other docs", Path: "/path/to/other"},
			},
			wantErr:     true,
			errContains: "duplicate name",
		},
		{
			name: "Missing name at second index",
			locations: []ContentLocation{
				{Name: "docs", Description: "Documentation", Path: "/path/to/docs"},
				{Name: "", Description: "Internal", Path: "/path/to/internal"},
			},
			wantErr:     true,
			errContains: "index 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateContentLocations(tt.locations)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateContentLocations() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
				}
			}
		})
	}
}

func TestContentLocation_YAMLParsing(t *testing.T) {
	t.Run("Parse single content location", func(t *testing.T) {
		yamlData := `
server:
  name: "Test Server"
  version: "1.0.0"
  instructions: "Test instructions"
content:
  - name: documentation
    description: "Public documentation"
    path: /path/to/docs
`
		var meta McpMetadata
		err := yaml.Unmarshal([]byte(yamlData), &meta)
		if err != nil {
			t.Fatalf("failed to parse YAML: %v", err)
		}

		if len(meta.Content) != 1 {
			t.Fatalf("expected 1 content location, got %d", len(meta.Content))
		}

		loc := meta.Content[0]
		if loc.Name != "documentation" {
			t.Errorf("expected name 'documentation', got %q", loc.Name)
		}
		if loc.Description != "Public documentation" {
			t.Errorf("expected description 'Public documentation', got %q", loc.Description)
		}
		if loc.Path != "/path/to/docs" {
			t.Errorf("expected path '/path/to/docs', got %q", loc.Path)
		}
	})

	t.Run("Parse multiple content locations", func(t *testing.T) {
		yamlData := `
server:
  name: "Test Server"
  version: "1.0.0"
  instructions: "Test instructions"
content:
  - name: documentation
    description: "Public documentation"
    path: /path/to/docs
  - name: internal
    description: "Internal guides"
    path: ./relative/path
`
		var meta McpMetadata
		err := yaml.Unmarshal([]byte(yamlData), &meta)
		if err != nil {
			t.Fatalf("failed to parse YAML: %v", err)
		}

		if len(meta.Content) != 2 {
			t.Fatalf("expected 2 content locations, got %d", len(meta.Content))
		}

		if meta.Content[0].Name != "documentation" {
			t.Errorf("expected first location name 'documentation', got %q", meta.Content[0].Name)
		}
		if meta.Content[1].Name != "internal" {
			t.Errorf("expected second location name 'internal', got %q", meta.Content[1].Name)
		}
		if meta.Content[1].Path != "./relative/path" {
			t.Errorf("expected second location path './relative/path', got %q", meta.Content[1].Path)
		}
	})

	t.Run("Parse with tools and content", func(t *testing.T) {
		yamlData := `
server:
  name: "Test Server"
  version: "1.0.0"
  instructions: "Test instructions"
tools:
  - name: search
    description: "Search tool"
content:
  - name: docs
    description: "Documentation"
    path: /docs
`
		var meta McpMetadata
		err := yaml.Unmarshal([]byte(yamlData), &meta)
		if err != nil {
			t.Fatalf("failed to parse YAML: %v", err)
		}

		if len(meta.Tools) != 1 {
			t.Errorf("expected 1 tool, got %d", len(meta.Tools))
		}
		if len(meta.Content) != 1 {
			t.Errorf("expected 1 content location, got %d", len(meta.Content))
		}
	})

	t.Run("Parse without content section", func(t *testing.T) {
		yamlData := `
server:
  name: "Test Server"
  version: "1.0.0"
  instructions: "Test instructions"
`
		var meta McpMetadata
		err := yaml.Unmarshal([]byte(yamlData), &meta)
		if err != nil {
			t.Fatalf("failed to parse YAML: %v", err)
		}

		if meta.Content != nil {
			t.Errorf("expected nil content, got %v", meta.Content)
		}
	})
}

func TestMcpMetadata_ValidateWithContent(t *testing.T) {
	t.Run("Valid metadata with content", func(t *testing.T) {
		meta := McpMetadata{
			Server: ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
			Content: []ContentLocation{
				{Name: "docs", Description: "Documentation", Path: "/path"},
			},
		}
		if err := meta.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("Valid metadata with multiple content locations", func(t *testing.T) {
		meta := McpMetadata{
			Server: ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
			Content: []ContentLocation{
				{Name: "docs", Description: "Documentation", Path: "/path1"},
				{Name: "internal", Description: "Internal", Path: "/path2"},
			},
		}
		if err := meta.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("Invalid - empty content", func(t *testing.T) {
		meta := McpMetadata{
			Server:  ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
			Content: []ContentLocation{},
		}
		err := meta.Validate()
		if err == nil {
			t.Error("expected error for empty content")
		}
	})

	t.Run("Invalid - content with missing name", func(t *testing.T) {
		meta := McpMetadata{
			Server: ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
			Content: []ContentLocation{
				{Name: "", Description: "Documentation", Path: "/path"},
			},
		}
		err := meta.Validate()
		if err == nil {
			t.Error("expected error for content with missing name")
		}
	})

	t.Run("Invalid - content with duplicate names", func(t *testing.T) {
		meta := McpMetadata{
			Server: ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
			Content: []ContentLocation{
				{Name: "docs", Description: "Documentation", Path: "/path1"},
				{Name: "docs", Description: "Other docs", Path: "/path2"},
			},
		}
		err := meta.Validate()
		if err == nil {
			t.Error("expected error for duplicate content names")
		}
		if !strings.Contains(err.Error(), "duplicate name") {
			t.Errorf("error should mention duplicate name: %v", err)
		}
	})

	t.Run("Valid - content with optional adapter type", func(t *testing.T) {
		meta := McpMetadata{
			Server: ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
			Content: []ContentLocation{
				{Name: "docs", Description: "Documentation", Path: "/path", Type: "acdc-mcp"},
			},
		}
		err := meta.Validate()
		if err != nil {
			t.Errorf("unexpected error for valid content with adapter type: %v", err)
		}
	})

	t.Run("Valid - content without adapter type (auto-detect)", func(t *testing.T) {
		meta := McpMetadata{
			Server: ServerMetadata{Name: "s", Version: "1", Instructions: "i"},
			Content: []ContentLocation{
				{Name: "docs", Description: "Documentation", Path: "/path"},
			},
		}
		err := meta.Validate()
		if err != nil {
			t.Errorf("unexpected error for valid content without adapter type: %v", err)
		}
	})
}
