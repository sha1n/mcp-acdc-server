package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
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

func TestCreateMCPServer_CrossRefTransformation(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	guidesDir := filepath.Join(resourcesDir, "guides")
	_ = os.MkdirAll(guidesDir, 0755)
	_ = os.MkdirAll(filepath.Join(contentDir, "mcp-prompts"), 0755)

	metadataContent := `server: { name: test, version: 1.0, instructions: inst }`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	// Resource A links to Resource B via relative markdown link
	resA := filepath.Join(resourcesDir, "doc-a.md")
	_ = os.WriteFile(resA, []byte("---\nname: Doc A\ndescription: Document A\n---\nSee [Doc B](guides/doc-b.md) for more."), 0644)

	// Resource B links back to A via parent-relative link
	resB := filepath.Join(guidesDir, "doc-b.md")
	_ = os.WriteFile(resB, []byte("---\nname: Doc B\ndescription: Document B\n---\nBack to [Doc A](../doc-a.md)."), 0644)

	settings := &config.Settings{
		ContentDir: contentDir,
		Scheme:     "acdc",
		CrossRef:   true,
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

func TestCreateMCPServer_CrossRefTransformation_ContentVerification(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	guidesDir := filepath.Join(resourcesDir, "guides")
	_ = os.MkdirAll(guidesDir, 0755)

	// Resource A links to Resource B via relative markdown link
	resA := filepath.Join(resourcesDir, "doc-a.md")
	_ = os.WriteFile(resA, []byte("---\nname: Doc A\ndescription: Document A\n---\nSee [Doc B](guides/doc-b.md) for more."), 0644)

	// Resource B links back to A via parent-relative link
	resB := filepath.Join(guidesDir, "doc-b.md")
	_ = os.WriteFile(resB, []byte("---\nname: Doc B\ndescription: Document B\n---\nBack to [Doc A](../doc-a.md)."), 0644)

	// Replicate the factory wiring to verify content transformation
	cp := content.NewContentProvider(contentDir)
	defs, err := resources.DiscoverResources(cp, "acdc")
	if err != nil {
		t.Fatalf("DiscoverResources error: %v", err)
	}

	transformer := resources.NewCrossRefTransformer(defs, "acdc")
	provider := resources.NewResourceProvider(defs, resources.WithTransformer(transformer))

	// Read Doc A - should have transformed link to Doc B
	contentA, err := provider.ReadResource("acdc://doc-a")
	if err != nil {
		t.Fatalf("ReadResource doc-a error: %v", err)
	}
	if !strings.Contains(contentA, "acdc://guides/doc-b") {
		t.Errorf("Doc A content should contain transformed URI 'acdc://guides/doc-b', got: %s", contentA)
	}
	if strings.Contains(contentA, "guides/doc-b.md") {
		t.Errorf("Doc A content should NOT contain original relative path 'guides/doc-b.md', got: %s", contentA)
	}

	// Read Doc B - should have transformed link back to Doc A
	contentB, err := provider.ReadResource("acdc://guides/doc-b")
	if err != nil {
		t.Fatalf("ReadResource doc-b error: %v", err)
	}
	if !strings.Contains(contentB, "acdc://doc-a") {
		t.Errorf("Doc B content should contain transformed URI 'acdc://doc-a', got: %s", contentB)
	}
	if strings.Contains(contentB, "../doc-a.md") {
		t.Errorf("Doc B content should NOT contain original relative path '../doc-a.md', got: %s", contentB)
	}
}

func TestCreateMCPServer_CrossRefDisabledByDefault(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	guidesDir := filepath.Join(resourcesDir, "guides")
	_ = os.MkdirAll(guidesDir, 0755)

	// Resource A links to Resource B via relative markdown link
	resA := filepath.Join(resourcesDir, "doc-a.md")
	_ = os.WriteFile(resA, []byte("---\nname: Doc A\ndescription: Document A\n---\nSee [Doc B](guides/doc-b.md) for more."), 0644)

	resB := filepath.Join(guidesDir, "doc-b.md")
	_ = os.WriteFile(resB, []byte("---\nname: Doc B\ndescription: Document B\n---\nBack to [Doc A](../doc-a.md)."), 0644)

	// CrossRef not set (defaults to false)
	cp := content.NewContentProvider(contentDir)
	defs, err := resources.DiscoverResources(cp, "acdc")
	if err != nil {
		t.Fatalf("DiscoverResources error: %v", err)
	}

	provider := resources.NewResourceProvider(defs)

	contentA, err := provider.ReadResource("acdc://doc-a")
	if err != nil {
		t.Fatalf("ReadResource doc-a error: %v", err)
	}
	if !strings.Contains(contentA, "guides/doc-b.md") {
		t.Errorf("With cross-ref disabled, Doc A should retain original relative link 'guides/doc-b.md', got: %s", contentA)
	}

	contentB, err := provider.ReadResource("acdc://guides/doc-b")
	if err != nil {
		t.Fatalf("ReadResource doc-b error: %v", err)
	}
	if !strings.Contains(contentB, "../doc-a.md") {
		t.Errorf("With cross-ref disabled, Doc B should retain original relative link '../doc-a.md', got: %s", contentB)
	}
}

func TestCreateMCPServer_CrossRefTransformation_CustomScheme(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	_ = os.MkdirAll(resourcesDir, 0755)

	resA := filepath.Join(resourcesDir, "a.md")
	_ = os.WriteFile(resA, []byte("---\nname: A\ndescription: A\n---\nSee [B](b.md)."), 0644)

	resB := filepath.Join(resourcesDir, "b.md")
	_ = os.WriteFile(resB, []byte("---\nname: B\ndescription: B\n---\nContent B."), 0644)

	cp := content.NewContentProvider(contentDir)
	defs, err := resources.DiscoverResources(cp, "myco")
	if err != nil {
		t.Fatalf("DiscoverResources error: %v", err)
	}

	transformer := resources.NewCrossRefTransformer(defs, "myco")
	provider := resources.NewResourceProvider(defs, resources.WithTransformer(transformer))

	contentA, err := provider.ReadResource("myco://a")
	if err != nil {
		t.Fatalf("ReadResource error: %v", err)
	}
	if !strings.Contains(contentA, "myco://b") {
		t.Errorf("Content should contain 'myco://b', got: %s", contentA)
	}
}
