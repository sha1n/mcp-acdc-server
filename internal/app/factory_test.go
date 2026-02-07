package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/config"
)

func TestCreateMCPServer_Success(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	promptsDir := filepath.Join(contentDir, "mcp-prompts")
	_ = os.MkdirAll(resourcesDir, 0755)
	_ = os.MkdirAll(promptsDir, 0755)

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

	promptFile := filepath.Join(promptsDir, "prompt.md")
	_ = os.WriteFile(promptFile, []byte("---\nname: prompt\ndescription: A test prompt\n---\nHello"), 0644)

	settings := &config.Settings{
		ContentDir: contentDir,
		Scheme:     "acdc",
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
	if !strings.Contains(err.Error(), "failed to read metadata file") {
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
	if !strings.Contains(err.Error(), "failed to parse metadata") {
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
	if !strings.Contains(err.Error(), "metadata validation failed") {
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
		Scheme:     "acdc",
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

func TestCreateMCPServer_ResourceWithKeywords(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	_ = os.MkdirAll(resourcesDir, 0755)

	metadataContent := `server: { name: test, version: 1.0, instructions: inst }`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	// Resource with keywords
	resFile := filepath.Join(resourcesDir, "res.md")
	_ = os.WriteFile(resFile, []byte("---\nname: res\ndescription: desc\nkeywords: k1,k2\n---\ncontent"), 0644)

	settings := &config.Settings{
		ContentDir: contentDir,
		Scheme:     "acdc",
		Search:     config.SearchSettings{InMemory: true, MaxResults: 10},
	}

	server, cleanup, err := CreateMCPServer(settings)
	if err != nil {
		t.Fatalf("Failed: %v", err)
	}
	defer cleanup()

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
		Scheme:     "acdc",
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

func TestCreateMCPServer_InvalidToolMetadata_MissingName(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	_ = os.MkdirAll(contentDir, 0755)

	metadataContent := `
server: { name: test, version: 1.0, instructions: inst }
tools:
  - name: ""
    description: "desc"
`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	settings := &config.Settings{ContentDir: contentDir}
	_, _, err := CreateMCPServer(settings)
	if err == nil || !strings.Contains(err.Error(), "metadata validation failed") {
		t.Errorf("Expected metadata validation error, got: %v", err)
	}
}

func TestCreateMCPServer_InvalidToolMetadata_MissingDescription(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	_ = os.MkdirAll(contentDir, 0755)

	metadataContent := `
server: { name: test, version: 1.0, instructions: inst }
tools:
  - name: "search"
    description: ""
`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	settings := &config.Settings{ContentDir: contentDir}
	_, _, err := CreateMCPServer(settings)
	if err == nil || !strings.Contains(err.Error(), "metadata validation failed") {
		t.Errorf("Expected metadata validation error, got: %v", err)
	}
}

func TestCreateMCPServer_InvalidToolMetadata_DuplicateNames(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	_ = os.MkdirAll(contentDir, 0755)

	metadataContent := `
server: { name: test, version: 1.0, instructions: inst }
tools:
  - { name: search, description: d1 }
  - { name: search, description: d2 }
`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	settings := &config.Settings{ContentDir: contentDir}
	_, _, err := CreateMCPServer(settings)
	if err == nil || !strings.Contains(err.Error(), "duplicate tool name") {
		t.Errorf("Expected duplicate tool name error, got: %v", err)
	}
}
func TestCreateMCPServer_PromptDiscoveryError(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	_ = os.MkdirAll(contentDir, 0755)

	metadataContent := `server: { name: test, version: 1.0, instructions: inst }`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	// Create resources dir so it doesn't fail here
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	_ = os.MkdirAll(resourcesDir, 0755)

	// Create a symlink loop to cause os.Stat to fail with "too many levels of symbolic links"
	promptsDir := filepath.Join(contentDir, "mcp-prompts")
	_ = os.Symlink(promptsDir, promptsDir)

	settings := &config.Settings{
		ContentDir: contentDir,
		Scheme:     "acdc",
		Search:     config.SearchSettings{InMemory: true},
	}

	_, _, err := CreateMCPServer(settings)
	if err == nil {
		t.Fatal("Expected error for prompt discovery failure")
	}
	if !strings.Contains(err.Error(), "failed to discover prompts") {
		t.Errorf("Unexpected error message: %v", err)
	}
}
