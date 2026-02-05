package adapters

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
)

// setupACDCTestDir creates a temporary directory with ACDC structure for testing
func setupACDCTestDir(t *testing.T, includePrompts bool) (string, *content.ContentProvider) {
	t.Helper()

	tmpDir := t.TempDir()

	// Create resources directory
	resourcesDir := filepath.Join(tmpDir, ACDCResourcesDir)
	if err := os.MkdirAll(resourcesDir, 0755); err != nil {
		t.Fatalf("failed to create resources dir: %v", err)
	}

	// Create a test resource
	resourceContent := `---
name: Test Resource
description: A test resource for ACDC adapter
keywords:
  - test
  - example
---

# Test Resource

This is the content of the test resource.
`
	resourceFile := filepath.Join(resourcesDir, "test-resource.md")
	if err := os.WriteFile(resourceFile, []byte(resourceContent), 0644); err != nil {
		t.Fatalf("failed to write resource file: %v", err)
	}

	// Create a nested resource
	nestedDir := filepath.Join(resourcesDir, "nested")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("failed to create nested dir: %v", err)
	}

	nestedContent := `---
name: Nested Resource
description: A nested test resource
---

# Nested Resource

Nested content.
`
	nestedFile := filepath.Join(nestedDir, "nested-resource.md")
	if err := os.WriteFile(nestedFile, []byte(nestedContent), 0644); err != nil {
		t.Fatalf("failed to write nested resource: %v", err)
	}

	// Create prompts directory if requested
	if includePrompts {
		promptsDir := filepath.Join(tmpDir, ACDCPromptsDir)
		if err := os.MkdirAll(promptsDir, 0755); err != nil {
			t.Fatalf("failed to create prompts dir: %v", err)
		}

		promptContent := `---
name: test-prompt
description: A test prompt template
arguments:
  - name: topic
    description: The topic to discuss
    required: true
---

Please explain {{.topic}} in detail.
`
		promptFile := filepath.Join(promptsDir, "test-prompt.md")
		if err := os.WriteFile(promptFile, []byte(promptContent), 0644); err != nil {
			t.Fatalf("failed to write prompt file: %v", err)
		}
	}

	// Create content provider
	cp, err := content.NewContentProvider(
		[]domain.ContentLocation{{Name: "test", Path: tmpDir}},
		tmpDir,
	)
	if err != nil {
		// The content provider will fail because we're using the new structure
		// but it still expects mcp-resources. For now, create a minimal provider.
		cp = &content.ContentProvider{}
	}

	return tmpDir, cp
}

// TestACDCAdapter_Name verifies the adapter name
func TestACDCAdapter_Name(t *testing.T) {
	adapter := NewACDCAdapter()

	if name := adapter.Name(); name != ACDCAdapterName {
		t.Errorf("Name() = %q, want %q", name, ACDCAdapterName)
	}
}

// TestACDCAdapter_CanHandle verifies directory structure detection
func TestACDCAdapter_CanHandle(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(t *testing.T) string
		expectHandle bool
	}{
		{
			name: "valid ACDC structure",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				resourcesDir := filepath.Join(dir, ACDCResourcesDir)
				if err := os.MkdirAll(resourcesDir, 0755); err != nil {
					t.Fatalf("failed to create resources dir: %v", err)
				}
				return dir
			},
			expectHandle: true,
		},
		{
			name: "missing resources directory",
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			expectHandle: false,
		},
		{
			name: "resources is a file not directory",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				resourcesFile := filepath.Join(dir, ACDCResourcesDir)
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
				return "/nonexistent/path"
			},
			expectHandle: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := NewACDCAdapter()
			path := tt.setup(t)

			got := adapter.CanHandle(path)
			if got != tt.expectHandle {
				t.Errorf("CanHandle() = %v, want %v", got, tt.expectHandle)
			}
		})
	}
}

// TestACDCAdapter_DiscoverResources verifies resource discovery
func TestACDCAdapter_DiscoverResources(t *testing.T) {
	t.Run("discover valid resources", func(t *testing.T) {
		tmpDir, cp := setupACDCTestDir(t, false)

		adapter := NewACDCAdapter()
		location := Location{
			Name:     "test",
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
			if def.Name == "Test Resource" {
				found = true
				if def.URI != "acdc://test/test-resource" {
					t.Errorf("URI = %q, want %q", def.URI, "acdc://test/test-resource")
				}
				if def.Description != "A test resource for ACDC adapter" {
					t.Errorf("Description = %q", def.Description)
				}
				if def.Source != "test" {
					t.Errorf("Source = %q, want %q", def.Source, "test")
				}
				if len(def.Keywords) != 2 {
					t.Errorf("Keywords length = %d, want 2", len(def.Keywords))
				}
				break
			}
		}
		if !found {
			t.Error("Test Resource not found in definitions")
		}

		// Check nested resource
		foundNested := false
		for _, def := range defs {
			if def.Name == "Nested Resource" {
				foundNested = true
				if def.URI != "acdc://test/nested/nested-resource" {
					t.Errorf("Nested URI = %q, want %q", def.URI, "acdc://test/nested/nested-resource")
				}
				break
			}
		}
		if !foundNested {
			t.Error("Nested Resource not found in definitions")
		}
	})

	t.Run("missing resources directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		cp := &content.ContentProvider{}

		adapter := NewACDCAdapter()
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
		resourcesDir := filepath.Join(tmpDir, ACDCResourcesDir)
		if err := os.MkdirAll(resourcesDir, 0755); err != nil {
			t.Fatalf("failed to create resources dir: %v", err)
		}

		cp := &content.ContentProvider{}
		adapter := NewACDCAdapter()
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

// TestACDCAdapter_DiscoverPrompts verifies prompt discovery
func TestACDCAdapter_DiscoverPrompts(t *testing.T) {
	t.Run("discover valid prompts", func(t *testing.T) {
		tmpDir, cp := setupACDCTestDir(t, true)

		adapter := NewACDCAdapter()
		location := Location{
			Name:     "test",
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
			if def.Name != "test:test-prompt" {
				t.Errorf("Name = %q, want %q", def.Name, "test:test-prompt")
			}
			if def.Description != "A test prompt template" {
				t.Errorf("Description = %q", def.Description)
			}
			if def.Source != "test" {
				t.Errorf("Source = %q, want %q", def.Source, "test")
			}
			if len(def.Arguments) != 1 {
				t.Errorf("Arguments length = %d, want 1", len(def.Arguments))
			} else {
				arg := def.Arguments[0]
				if arg.Name != "topic" {
					t.Errorf("Argument name = %q, want %q", arg.Name, "topic")
				}
				if !arg.Required {
					t.Error("Argument should be required")
				}
			}
		}
	})

	t.Run("missing prompts directory is ok", func(t *testing.T) {
		tmpDir, cp := setupACDCTestDir(t, false)

		adapter := NewACDCAdapter()
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
		resourcesDir := filepath.Join(tmpDir, ACDCResourcesDir)
		if err := os.MkdirAll(resourcesDir, 0755); err != nil {
			t.Fatalf("failed to create resources dir: %v", err)
		}
		promptsDir := filepath.Join(tmpDir, ACDCPromptsDir)
		if err := os.MkdirAll(promptsDir, 0755); err != nil {
			t.Fatalf("failed to create prompts dir: %v", err)
		}

		cp := &content.ContentProvider{}
		adapter := NewACDCAdapter()
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

// TestACDCAdapter_IntegrationScenario tests a complete usage scenario
func TestACDCAdapter_IntegrationScenario(t *testing.T) {
	tmpDir, cp := setupACDCTestDir(t, true)

	adapter := NewACDCAdapter()

	// Verify it can handle the structure
	if !adapter.CanHandle(tmpDir) {
		t.Fatal("CanHandle() returned false for valid structure")
	}

	location := Location{
		Name:     "docs",
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
		if r.Source != "docs" {
			t.Errorf("Resource source = %q, want %q", r.Source, "docs")
		}
	}

	// Verify prompt names are namespaced
	for _, p := range prompts {
		if p.Source != "docs" {
			t.Errorf("Prompt source = %q, want %q", p.Source, "docs")
		}
	}
}
