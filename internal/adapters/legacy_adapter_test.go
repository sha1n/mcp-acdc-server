package adapters

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/content"
)

// setupLegacyTestDir creates a temporary directory with legacy structure for testing
func setupLegacyTestDir(t *testing.T, includePrompts bool) (string, *content.ContentProvider) {
	t.Helper()

	tmpDir := t.TempDir()

	// Create mcp-resources directory
	resourcesDir := filepath.Join(tmpDir, LegacyResourcesDir)
	if err := os.MkdirAll(resourcesDir, 0755); err != nil {
		t.Fatalf("failed to create resources dir: %v", err)
	}

	// Create a test resource
	resourceContent := `---
name: Legacy Resource
description: A test resource for legacy adapter
keywords:
  - legacy
  - test
---

# Legacy Resource

This is the content of the legacy resource.
`
	resourceFile := filepath.Join(resourcesDir, "legacy-resource.md")
	if err := os.WriteFile(resourceFile, []byte(resourceContent), 0644); err != nil {
		t.Fatalf("failed to write resource file: %v", err)
	}

	// Create a nested resource
	nestedDir := filepath.Join(resourcesDir, "subdir")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("failed to create nested dir: %v", err)
	}

	nestedContent := `---
name: Nested Legacy Resource
description: A nested legacy resource
---

# Nested Legacy Resource

Nested content in legacy structure.
`
	nestedFile := filepath.Join(nestedDir, "nested.md")
	if err := os.WriteFile(nestedFile, []byte(nestedContent), 0644); err != nil {
		t.Fatalf("failed to write nested resource: %v", err)
	}

	// Create mcp-prompts directory if requested
	if includePrompts {
		promptsDir := filepath.Join(tmpDir, LegacyPromptsDir)
		if err := os.MkdirAll(promptsDir, 0755); err != nil {
			t.Fatalf("failed to create prompts dir: %v", err)
		}

		promptContent := `---
name: legacy-prompt
description: A legacy prompt template
arguments:
  - name: query
    description: The query to process
    required: true
---

Process this query: {{.query}}
`
		promptFile := filepath.Join(promptsDir, "legacy-prompt.md")
		if err := os.WriteFile(promptFile, []byte(promptContent), 0644); err != nil {
			t.Fatalf("failed to write prompt file: %v", err)
		}
	}

	// Create a minimal content provider
	cp := &content.ContentProvider{}

	return tmpDir, cp
}

// TestLegacyAdapter_Name verifies the adapter name
func TestLegacyAdapter_Name(t *testing.T) {
	adapter := NewLegacyAdapter()

	if name := adapter.Name(); name != LegacyAdapterName {
		t.Errorf("Name() = %q, want %q", name, LegacyAdapterName)
	}
}

// TestLegacyAdapter_CanHandle verifies directory structure detection
func TestLegacyAdapter_CanHandle(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(t *testing.T) string
		expectHandle bool
	}{
		{
			name: "valid legacy structure",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				resourcesDir := filepath.Join(dir, LegacyResourcesDir)
				if err := os.MkdirAll(resourcesDir, 0755); err != nil {
					t.Fatalf("failed to create resources dir: %v", err)
				}
				return dir
			},
			expectHandle: true,
		},
		{
			name: "missing mcp-resources directory",
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			expectHandle: false,
		},
		{
			name: "mcp-resources is a file not directory",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				resourcesFile := filepath.Join(dir, LegacyResourcesDir)
				if err := os.WriteFile(resourcesFile, []byte("not a dir"), 0644); err != nil {
					t.Fatalf("failed to create resources file: %v", err)
				}
				return dir
			},
			expectHandle: false,
		},
		{
			name: "nonexistent path",
			setup: func(t *testing.T) string {
				return "/nonexistent/legacy/path"
			},
			expectHandle: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := NewLegacyAdapter()
			path := tt.setup(t)

			got := adapter.CanHandle(path)
			if got != tt.expectHandle {
				t.Errorf("CanHandle() = %v, want %v", got, tt.expectHandle)
			}
		})
	}
}

// TestLegacyAdapter_DiscoverResources verifies resource discovery
func TestLegacyAdapter_DiscoverResources(t *testing.T) {
	t.Run("discover valid resources", func(t *testing.T) {
		tmpDir, cp := setupLegacyTestDir(t, false)

		adapter := NewLegacyAdapter()
		location := Location{
			Name:     "legacy-test",
			BasePath: tmpDir,
		}

		defs, err := adapter.DiscoverResources(location, cp)
		if err != nil {
			t.Fatalf("DiscoverResources() error = %v", err)
		}

		if len(defs) != 2 {
			t.Errorf("DiscoverResources() returned %d definitions, want 2", len(defs))
		}

		// Check first resource
		found := false
		for _, def := range defs {
			if def.Name == "Legacy Resource" {
				found = true
				if def.URI != "acdc://legacy-test/legacy-resource" {
					t.Errorf("URI = %q, want %q", def.URI, "acdc://legacy-test/legacy-resource")
				}
				if def.Description != "A test resource for legacy adapter" {
					t.Errorf("Description = %q", def.Description)
				}
				if def.Source != "legacy-test" {
					t.Errorf("Source = %q, want %q", def.Source, "legacy-test")
				}
				if len(def.Keywords) != 2 {
					t.Errorf("Keywords length = %d, want 2", len(def.Keywords))
				}
				break
			}
		}
		if !found {
			t.Error("Legacy Resource not found in definitions")
		}

		// Check nested resource
		foundNested := false
		for _, def := range defs {
			if def.Name == "Nested Legacy Resource" {
				foundNested = true
				if def.URI != "acdc://legacy-test/subdir/nested" {
					t.Errorf("Nested URI = %q, want %q", def.URI, "acdc://legacy-test/subdir/nested")
				}
				break
			}
		}
		if !foundNested {
			t.Error("Nested Legacy Resource not found in definitions")
		}
	})

	t.Run("missing resources directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		cp := &content.ContentProvider{}

		adapter := NewLegacyAdapter()
		location := Location{
			Name:     "test",
			BasePath: tmpDir,
		}

		_, err := adapter.DiscoverResources(location, cp)
		if err == nil {
			t.Error("DiscoverResources() expected error for missing directory")
		}
	})

	t.Run("empty resources directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		resourcesDir := filepath.Join(tmpDir, LegacyResourcesDir)
		if err := os.MkdirAll(resourcesDir, 0755); err != nil {
			t.Fatalf("failed to create resources dir: %v", err)
		}

		cp := &content.ContentProvider{}
		adapter := NewLegacyAdapter()
		location := Location{
			Name:     "test",
			BasePath: tmpDir,
		}

		defs, err := adapter.DiscoverResources(location, cp)
		if err != nil {
			t.Fatalf("DiscoverResources() error = %v", err)
		}

		if len(defs) != 0 {
			t.Errorf("DiscoverResources() returned %d definitions, want 0", len(defs))
		}
	})
}

// TestLegacyAdapter_DiscoverPrompts verifies prompt discovery
func TestLegacyAdapter_DiscoverPrompts(t *testing.T) {
	t.Run("discover valid prompts", func(t *testing.T) {
		tmpDir, cp := setupLegacyTestDir(t, true)

		adapter := NewLegacyAdapter()
		location := Location{
			Name:     "legacy-test",
			BasePath: tmpDir,
		}

		defs, err := adapter.DiscoverPrompts(location, cp)
		if err != nil {
			t.Fatalf("DiscoverPrompts() error = %v", err)
		}

		if len(defs) != 1 {
			t.Errorf("DiscoverPrompts() returned %d definitions, want 1", len(defs))
		}

		if len(defs) > 0 {
			def := defs[0]
			if def.Name != "legacy-test:legacy-prompt" {
				t.Errorf("Name = %q, want %q", def.Name, "legacy-test:legacy-prompt")
			}
			if def.Description != "A legacy prompt template" {
				t.Errorf("Description = %q", def.Description)
			}
			if def.Source != "legacy-test" {
				t.Errorf("Source = %q, want %q", def.Source, "legacy-test")
			}
			if len(def.Arguments) != 1 {
				t.Errorf("Arguments length = %d, want 1", len(def.Arguments))
			} else {
				arg := def.Arguments[0]
				if arg.Name != "query" {
					t.Errorf("Argument name = %q, want %q", arg.Name, "query")
				}
				if !arg.Required {
					t.Error("Argument should be required")
				}
			}
		}
	})

	t.Run("missing prompts directory is ok", func(t *testing.T) {
		tmpDir, cp := setupLegacyTestDir(t, false)

		adapter := NewLegacyAdapter()
		location := Location{
			Name:     "test",
			BasePath: tmpDir,
		}

		defs, err := adapter.DiscoverPrompts(location, cp)
		if err != nil {
			t.Fatalf("DiscoverPrompts() error = %v", err)
		}

		if len(defs) != 0 {
			t.Errorf("DiscoverPrompts() returned %d definitions, want 0 for missing prompts dir", len(defs))
		}
	})

	t.Run("empty prompts directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		resourcesDir := filepath.Join(tmpDir, LegacyResourcesDir)
		if err := os.MkdirAll(resourcesDir, 0755); err != nil {
			t.Fatalf("failed to create resources dir: %v", err)
		}
		promptsDir := filepath.Join(tmpDir, LegacyPromptsDir)
		if err := os.MkdirAll(promptsDir, 0755); err != nil {
			t.Fatalf("failed to create prompts dir: %v", err)
		}

		cp := &content.ContentProvider{}
		adapter := NewLegacyAdapter()
		location := Location{
			Name:     "test",
			BasePath: tmpDir,
		}

		defs, err := adapter.DiscoverPrompts(location, cp)
		if err != nil {
			t.Fatalf("DiscoverPrompts() error = %v", err)
		}

		if len(defs) != 0 {
			t.Errorf("DiscoverPrompts() returned %d definitions, want 0", len(defs))
		}
	})
}

// TestLegacyAdapter_IntegrationScenario tests a complete usage scenario
func TestLegacyAdapter_IntegrationScenario(t *testing.T) {
	tmpDir, cp := setupLegacyTestDir(t, true)

	adapter := NewLegacyAdapter()

	// Verify it can handle the structure
	if !adapter.CanHandle(tmpDir) {
		t.Fatal("CanHandle() returned false for valid legacy structure")
	}

	location := Location{
		Name:     "legacy-docs",
		BasePath: tmpDir,
	}

	// Discover resources
	resources, err := adapter.DiscoverResources(location, cp)
	if err != nil {
		t.Fatalf("DiscoverResources() error = %v", err)
	}
	if len(resources) == 0 {
		t.Error("Expected resources to be discovered")
	}

	// Discover prompts
	prompts, err := adapter.DiscoverPrompts(location, cp)
	if err != nil {
		t.Fatalf("DiscoverPrompts() error = %v", err)
	}
	if len(prompts) == 0 {
		t.Error("Expected prompts to be discovered")
	}

	// Verify URIs use correct source
	for _, r := range resources {
		if r.Source != "legacy-docs" {
			t.Errorf("Resource source = %q, want %q", r.Source, "legacy-docs")
		}
	}

	// Verify prompt names are namespaced
	for _, p := range prompts {
		if p.Source != "legacy-docs" {
			t.Errorf("Prompt source = %q, want %q", p.Source, "legacy-docs")
		}
	}
}

// TestLegacyAdapter_ACDCCompatibility verifies both adapters don't conflict
func TestLegacyAdapter_ACDCCompatibility(t *testing.T) {
	t.Run("legacy adapter ignores ACDC structure", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create ACDC structure
		resourcesDir := filepath.Join(tmpDir, ACDCResourcesDir)
		if err := os.MkdirAll(resourcesDir, 0755); err != nil {
			t.Fatalf("failed to create ACDC resources dir: %v", err)
		}

		adapter := NewLegacyAdapter()

		// Legacy adapter should not handle ACDC structure
		if adapter.CanHandle(tmpDir) {
			t.Error("Legacy adapter should not handle ACDC structure")
		}
	})

	t.Run("ACDC adapter ignores legacy structure", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create legacy structure
		resourcesDir := filepath.Join(tmpDir, LegacyResourcesDir)
		if err := os.MkdirAll(resourcesDir, 0755); err != nil {
			t.Fatalf("failed to create legacy resources dir: %v", err)
		}

		adapter := NewACDCAdapter()

		// ACDC adapter should not handle legacy structure
		if adapter.CanHandle(tmpDir) {
			t.Error("ACDC adapter should not handle legacy structure")
		}
	})
}
