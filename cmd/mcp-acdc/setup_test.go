package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sha1n/mcp-acdc-server-go/internal/config"
)

func TestCreateMCPServer_Success(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	_ = os.MkdirAll(resourcesDir, 0755)

	metadataContent := `
server:
  name: test
  version: 1.0
  instructions: inst
tools: []
`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	resFile := filepath.Join(resourcesDir, "res.md")
	_ = os.WriteFile(resFile, []byte("---\nname: res\ndescription: A test resource\n---\ncontent"), 0644)

	settings := &config.Settings{
		ContentDir: contentDir,
		Search: config.SearchSettings{
			InMemory:   true,
			MaxResults: 10,
		},
	}

	server, cleanup, err := CreateMCPServer(settings)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer cleanup()

	if server == nil {
		t.Fatal("Server is nil")
	}
}

func TestCreateMCPServer_MissingMetadata(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	_ = os.MkdirAll(contentDir, 0755)

	settings := &config.Settings{
		ContentDir: contentDir,
		Search: config.SearchSettings{
			InMemory:   true,
			MaxResults: 10,
		},
	}

	_, _, err := CreateMCPServer(settings)
	if err == nil {
		t.Fatal("Expected error when metadata is missing")
	}
	if !contains(err.Error(), "failed to read metadata file") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestCreateMCPServer_InvalidMetadataYAML(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	_ = os.MkdirAll(contentDir, 0755)

	// Write invalid YAML
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte("not: valid: yaml: {{"), 0644)

	settings := &config.Settings{
		ContentDir: contentDir,
		Search: config.SearchSettings{
			InMemory:   true,
			MaxResults: 10,
		},
	}

	_, _, err := CreateMCPServer(settings)
	if err == nil {
		t.Fatal("Expected error for invalid YAML")
	}
	if !contains(err.Error(), "failed to parse metadata") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestCreateMCPServer_MetadataValidationFails(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	_ = os.MkdirAll(contentDir, 0755)

	// Empty metadata fails validation
	metadataContent := `
server:
  name: ""
  version: ""
  instructions: ""
`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	settings := &config.Settings{
		ContentDir: contentDir,
		Search: config.SearchSettings{
			InMemory:   true,
			MaxResults: 10,
		},
	}

	_, _, err := CreateMCPServer(settings)
	if err == nil {
		t.Fatal("Expected error for invalid metadata")
	}
	if !contains(err.Error(), "metadata validation failed") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestCreateMCPServer_InvalidResourceIsSkipped(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	_ = os.MkdirAll(resourcesDir, 0755)

	metadataContent := `
server:
  name: test
  version: 1.0
  instructions: inst
tools: []
`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	// Write an invalid resource file (invalid frontmatter) - should be skipped with warning
	_ = os.WriteFile(filepath.Join(resourcesDir, "invalid.md"), []byte("---\n: broken\n---\ncontent"), 0644)

	settings := &config.Settings{
		ContentDir: contentDir,
		Search: config.SearchSettings{
			InMemory:   true,
			MaxResults: 10,
		},
	}

	// Invalid resources are skipped, not failed
	server, cleanup, err := CreateMCPServer(settings)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if server == nil {
		t.Fatal("Server is nil")
	}
}

func TestCreateMCPServer_NoResources(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	_ = os.MkdirAll(resourcesDir, 0755)

	metadataContent := `
server:
  name: test
  version: 1.0
  instructions: inst
tools: []
`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	settings := &config.Settings{
		ContentDir: contentDir,
		Search: config.SearchSettings{
			InMemory:   true,
			MaxResults: 10,
		},
	}

	// Should succeed with no resources
	server, cleanup, err := CreateMCPServer(settings)
	if err != nil {
		t.Fatalf("Failed to create server with no resources: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if server == nil {
		t.Fatal("Server is nil")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
