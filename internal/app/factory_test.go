package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/config"
)

func createTestConfigFile(t *testing.T, tempDir, contentDir string, metadataContent string) string {
	t.Helper()
	configPath := filepath.Join(tempDir, "mcp-metadata.yaml")
	err := os.WriteFile(configPath, []byte(metadataContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}
	return configPath
}

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
content:
  - name: docs
    description: Documentation
    path: content
`
	configPath := createTestConfigFile(t, tempDir, contentDir, metadataContent)

	resFile := filepath.Join(resourcesDir, "res.md")
	_ = os.WriteFile(resFile, []byte("---\nname: res\ndescription: A test resource\n---\ncontent"), 0644)

	promptFile := filepath.Join(promptsDir, "prompt.md")
	_ = os.WriteFile(promptFile, []byte("---\nname: prompt\ndescription: A test prompt\n---\nHello"), 0644)

	settings := &config.Settings{
		ConfigPath: configPath,
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

func TestCreateMCPServer_MissingConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "nonexistent.yaml")

	settings := &config.Settings{
		ConfigPath: configPath,
		Search: config.SearchSettings{
			InMemory:   true,
			MaxResults: 10,
		},
	}

	_, _, err := CreateMCPServer(settings)
	if err == nil {
		t.Fatal("Expected error when config is missing")
	}
	if !strings.Contains(err.Error(), "failed to read config file") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestCreateMCPServer_InvalidConfigYAML(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "mcp-metadata.yaml")

	// Write invalid YAML
	_ = os.WriteFile(configPath, []byte("not: valid: yaml: {{"), 0644)

	settings := &config.Settings{
		ConfigPath: configPath,
		Search: config.SearchSettings{
			InMemory:   true,
			MaxResults: 10,
		},
	}

	_, _, err := CreateMCPServer(settings)
	if err == nil {
		t.Fatal("Expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "failed to parse config") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestCreateMCPServer_ConfigValidationFails(t *testing.T) {
	tempDir := t.TempDir()

	// Empty metadata fails validation
	metadataContent := `
server:
  name: ""
  version: ""
  instructions: ""
`
	configPath := createTestConfigFile(t, tempDir, tempDir, metadataContent)

	settings := &config.Settings{
		ConfigPath: configPath,
		Search: config.SearchSettings{
			InMemory:   true,
			MaxResults: 10,
		},
	}

	_, _, err := CreateMCPServer(settings)
	if err == nil {
		t.Fatal("Expected error for invalid config")
	}
	if !strings.Contains(err.Error(), "config validation failed") {
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
content:
  - name: docs
    description: Documentation
    path: content
`
	configPath := createTestConfigFile(t, tempDir, contentDir, metadataContent)

	// Write an invalid resource file (invalid frontmatter) - should be skipped with warning
	_ = os.WriteFile(filepath.Join(resourcesDir, "invalid.md"), []byte("---\n: broken\n---\ncontent"), 0644)

	settings := &config.Settings{
		ConfigPath: configPath,
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

	metadataContent := `
server:
  name: test
  version: 1.0
  instructions: inst
content:
  - name: docs
    description: Documentation
    path: content
`
	configPath := createTestConfigFile(t, tempDir, contentDir, metadataContent)

	// Resource with keywords
	resFile := filepath.Join(resourcesDir, "res.md")
	_ = os.WriteFile(resFile, []byte("---\nname: res\ndescription: desc\nkeywords: k1,k2\n---\ncontent"), 0644)

	settings := &config.Settings{
		ConfigPath: configPath,
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
content:
  - name: docs
    description: Documentation
    path: content
`
	configPath := createTestConfigFile(t, tempDir, contentDir, metadataContent)

	settings := &config.Settings{
		ConfigPath: configPath,
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

	metadataContent := `
server:
  name: test
  version: 1.0
  instructions: inst
tools:
  - name: ""
    description: "desc"
`
	configPath := createTestConfigFile(t, tempDir, tempDir, metadataContent)

	settings := &config.Settings{ConfigPath: configPath}
	_, _, err := CreateMCPServer(settings)
	if err == nil || !strings.Contains(err.Error(), "config validation failed") {
		t.Errorf("Expected config validation error, got: %v", err)
	}
}

func TestCreateMCPServer_InvalidToolMetadata_MissingDescription(t *testing.T) {
	tempDir := t.TempDir()

	metadataContent := `
server:
  name: test
  version: 1.0
  instructions: inst
tools:
  - name: "search"
    description: ""
`
	configPath := createTestConfigFile(t, tempDir, tempDir, metadataContent)

	settings := &config.Settings{ConfigPath: configPath}
	_, _, err := CreateMCPServer(settings)
	if err == nil || !strings.Contains(err.Error(), "config validation failed") {
		t.Errorf("Expected config validation error, got: %v", err)
	}
}

func TestCreateMCPServer_InvalidToolMetadata_DuplicateNames(t *testing.T) {
	tempDir := t.TempDir()

	metadataContent := `
server:
  name: test
  version: 1.0
  instructions: inst
tools:
  - { name: search, description: d1 }
  - { name: search, description: d2 }
`
	configPath := createTestConfigFile(t, tempDir, tempDir, metadataContent)

	settings := &config.Settings{ConfigPath: configPath}
	_, _, err := CreateMCPServer(settings)
	if err == nil || !strings.Contains(err.Error(), "duplicate tool name") {
		t.Errorf("Expected duplicate tool name error, got: %v", err)
	}
}

func TestCreateMCPServer_MissingResourcesDir(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	_ = os.MkdirAll(contentDir, 0755)

	metadataContent := `
server:
  name: test
  version: 1.0
  instructions: inst
content:
  - name: docs
    description: Documentation
    path: content
`
	configPath := createTestConfigFile(t, tempDir, contentDir, metadataContent)

	// Note: NOT creating mcp-resources/ directory - this should cause content provider init to fail

	settings := &config.Settings{
		ConfigPath: configPath,
		Search:     config.SearchSettings{InMemory: true},
	}

	_, _, err := CreateMCPServer(settings)
	if err == nil {
		t.Fatal("Expected error for missing resources directory")
	}
	if !strings.Contains(err.Error(), "failed to initialize content provider") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestCreateMCPServer_InvalidContentLocation(t *testing.T) {
	tempDir := t.TempDir()

	metadataContent := `
server:
  name: test
  version: 1.0
  instructions: inst
content:
  - name: ""
    description: Documentation
    path: content
`
	configPath := createTestConfigFile(t, tempDir, tempDir, metadataContent)

	settings := &config.Settings{ConfigPath: configPath}
	_, _, err := CreateMCPServer(settings)
	if err == nil || !strings.Contains(err.Error(), "config validation failed") {
		t.Errorf("Expected config validation error, got: %v", err)
	}
}

func TestCreateMCPServer_EmptyContent(t *testing.T) {
	tempDir := t.TempDir()

	metadataContent := `
server:
  name: test
  version: 1.0
  instructions: inst
content: []
`
	configPath := createTestConfigFile(t, tempDir, tempDir, metadataContent)

	settings := &config.Settings{
		ConfigPath: configPath,
		Search:     config.SearchSettings{InMemory: true, MaxResults: 10},
	}

	// Empty content is now invalid - at least one location is required
	_, _, err := CreateMCPServer(settings)
	if err == nil {
		t.Fatal("Expected error for empty content")
	}
	if !strings.Contains(err.Error(), "config validation failed") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestCreateMCPServer_MultipleContentLocations(t *testing.T) {
	tempDir := t.TempDir()

	// Create two content locations
	content1Dir := filepath.Join(tempDir, "content1")
	content2Dir := filepath.Join(tempDir, "content2")
	resources1Dir := filepath.Join(content1Dir, "mcp-resources")
	resources2Dir := filepath.Join(content2Dir, "mcp-resources")
	_ = os.MkdirAll(resources1Dir, 0755)
	_ = os.MkdirAll(resources2Dir, 0755)

	// Create resources in each location
	_ = os.WriteFile(filepath.Join(resources1Dir, "res1.md"), []byte("---\nname: res1\ndescription: Resource 1\n---\nContent 1"), 0644)
	_ = os.WriteFile(filepath.Join(resources2Dir, "res2.md"), []byte("---\nname: res2\ndescription: Resource 2\n---\nContent 2"), 0644)

	metadataContent := `
server:
  name: test
  version: 1.0
  instructions: inst
content:
  - name: loc1
    description: Location 1
    path: content1
  - name: loc2
    description: Location 2
    path: content2
`
	configPath := createTestConfigFile(t, tempDir, tempDir, metadataContent)

	settings := &config.Settings{
		ConfigPath: configPath,
		Search:     config.SearchSettings{InMemory: true, MaxResults: 10},
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
