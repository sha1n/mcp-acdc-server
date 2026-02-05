package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
)

// Helper to create a content location directory structure
func createContentLocation(t *testing.T, basePath string, withPrompts bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(basePath, "mcp-resources"), 0755); err != nil {
		t.Fatal(err)
	}
	if withPrompts {
		if err := os.MkdirAll(filepath.Join(basePath, "mcp-prompts"), 0755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNewContentProvider_SingleLocation(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	createContentLocation(t, loc1, false)

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
	}

	p, err := NewContentProvider(locations, tempDir)
	if err != nil {
		t.Fatalf("NewContentProvider failed: %v", err)
	}

	resourceLocs := p.ResourceLocations()
	if len(resourceLocs) != 1 {
		t.Fatalf("Expected 1 resource location, got %d", len(resourceLocs))
	}
	if resourceLocs[0].Name != "docs" {
		t.Errorf("Expected name 'docs', got '%s'", resourceLocs[0].Name)
	}
	if resourceLocs[0].Path != filepath.Join(loc1, "mcp-resources") {
		t.Errorf("Unexpected resource path: %s", resourceLocs[0].Path)
	}
}

func TestNewContentProvider_MultipleLocations(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	loc2 := filepath.Join(tempDir, "internal")
	createContentLocation(t, loc1, true)
	createContentLocation(t, loc2, false)

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
		{Name: "internal", Description: "Internal guides", Path: loc2},
	}

	p, err := NewContentProvider(locations, tempDir)
	if err != nil {
		t.Fatalf("NewContentProvider failed: %v", err)
	}

	resourceLocs := p.ResourceLocations()
	if len(resourceLocs) != 2 {
		t.Fatalf("Expected 2 resource locations, got %d", len(resourceLocs))
	}

	promptLocs := p.PromptLocations()
	if len(promptLocs) != 1 {
		t.Fatalf("Expected 1 prompt location (only docs has prompts), got %d", len(promptLocs))
	}
	if promptLocs[0].Name != "docs" {
		t.Errorf("Expected prompt location name 'docs', got '%s'", promptLocs[0].Name)
	}
}

func TestNewContentProvider_RelativePath(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "content", "docs")
	createContentLocation(t, loc1, false)

	// Use relative path
	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: "./content/docs"},
	}

	p, err := NewContentProvider(locations, tempDir)
	if err != nil {
		t.Fatalf("NewContentProvider failed: %v", err)
	}

	resourceLocs := p.ResourceLocations()
	if len(resourceLocs) != 1 {
		t.Fatalf("Expected 1 resource location, got %d", len(resourceLocs))
	}

	// Should resolve to absolute path
	expectedPath := filepath.Join(tempDir, "content", "docs", "mcp-resources")
	if resourceLocs[0].Path != expectedPath {
		t.Errorf("Expected path '%s', got '%s'", expectedPath, resourceLocs[0].Path)
	}
}

func TestNewContentProvider_AbsolutePath(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	createContentLocation(t, loc1, false)

	// Use absolute path
	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
	}

	p, err := NewContentProvider(locations, "/some/other/dir")
	if err != nil {
		t.Fatalf("NewContentProvider failed: %v", err)
	}

	resourceLocs := p.ResourceLocations()
	// Should use absolute path as-is, not relative to configDir
	expectedPath := filepath.Join(loc1, "mcp-resources")
	if resourceLocs[0].Path != expectedPath {
		t.Errorf("Expected path '%s', got '%s'", expectedPath, resourceLocs[0].Path)
	}
}

func TestNewContentProvider_MixedPaths(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "absolute-docs")
	loc2Path := filepath.Join(tempDir, "relative-docs")
	createContentLocation(t, loc1, false)
	createContentLocation(t, loc2Path, false)

	locations := []domain.ContentLocation{
		{Name: "absolute", Description: "Absolute path", Path: loc1},
		{Name: "relative", Description: "Relative path", Path: "./relative-docs"},
	}

	p, err := NewContentProvider(locations, tempDir)
	if err != nil {
		t.Fatalf("NewContentProvider failed: %v", err)
	}

	resourceLocs := p.ResourceLocations()
	if len(resourceLocs) != 2 {
		t.Fatalf("Expected 2 resource locations, got %d", len(resourceLocs))
	}
}

func TestNewContentProvider_PathNotExist(t *testing.T) {
	tempDir := t.TempDir()

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: "/non/existent/path"},
	}

	_, err := NewContentProvider(locations, tempDir)
	if err == nil {
		t.Fatal("Expected error for non-existent path")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("Error should mention 'does not exist': %v", err)
	}
}

func TestNewContentProvider_NoResourcesDir(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	// Create directory but no mcp-resources/
	if err := os.MkdirAll(loc1, 0755); err != nil {
		t.Fatal(err)
	}

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
	}

	_, err := NewContentProvider(locations, tempDir)
	if err == nil {
		t.Fatal("Expected error for missing mcp-resources/")
	}
	if !strings.Contains(err.Error(), "missing mcp-resources") {
		t.Errorf("Error should mention 'missing mcp-resources': %v", err)
	}
}

func TestNewContentProvider_EmptyResourcesDir(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	createContentLocation(t, loc1, false) // Empty mcp-resources/ is OK

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
	}

	p, err := NewContentProvider(locations, tempDir)
	if err != nil {
		t.Fatalf("Expected success for empty mcp-resources/, got: %v", err)
	}
	if p == nil {
		t.Fatal("Provider should not be nil")
	}
}

func TestNewContentProvider_NoPromptsDir(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	createContentLocation(t, loc1, false) // No prompts dir

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
	}

	p, err := NewContentProvider(locations, tempDir)
	if err != nil {
		t.Fatalf("Expected success without mcp-prompts/, got: %v", err)
	}

	promptLocs := p.PromptLocations()
	if len(promptLocs) != 0 {
		t.Errorf("Expected 0 prompt locations, got %d", len(promptLocs))
	}
}

func TestNewContentProvider_ResourceLocations(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	loc2 := filepath.Join(tempDir, "internal")
	createContentLocation(t, loc1, false)
	createContentLocation(t, loc2, false)

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
		{Name: "internal", Description: "Internal", Path: loc2},
	}

	p, err := NewContentProvider(locations, tempDir)
	if err != nil {
		t.Fatalf("NewContentProvider failed: %v", err)
	}

	resourceLocs := p.ResourceLocations()

	// Verify order is preserved
	if resourceLocs[0].Name != "docs" {
		t.Errorf("First location should be 'docs', got '%s'", resourceLocs[0].Name)
	}
	if resourceLocs[1].Name != "internal" {
		t.Errorf("Second location should be 'internal', got '%s'", resourceLocs[1].Name)
	}

	// Verify paths
	if !strings.HasSuffix(resourceLocs[0].Path, "mcp-resources") {
		t.Errorf("Resource path should end with mcp-resources: %s", resourceLocs[0].Path)
	}
}

func TestNewContentProvider_PromptLocations(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	loc2 := filepath.Join(tempDir, "internal")
	loc3 := filepath.Join(tempDir, "api")
	createContentLocation(t, loc1, true)  // Has prompts
	createContentLocation(t, loc2, false) // No prompts
	createContentLocation(t, loc3, true)  // Has prompts

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
		{Name: "internal", Description: "Internal", Path: loc2},
		{Name: "api", Description: "API", Path: loc3},
	}

	p, err := NewContentProvider(locations, tempDir)
	if err != nil {
		t.Fatalf("NewContentProvider failed: %v", err)
	}

	promptLocs := p.PromptLocations()
	if len(promptLocs) != 2 {
		t.Fatalf("Expected 2 prompt locations, got %d", len(promptLocs))
	}

	// Verify only locations with prompts are returned
	names := make(map[string]bool)
	for _, loc := range promptLocs {
		names[loc.Name] = true
	}
	if !names["docs"] || !names["api"] {
		t.Errorf("Expected 'docs' and 'api', got %v", names)
	}
	if names["internal"] {
		t.Error("'internal' should not be in prompt locations")
	}
}

func TestNewContentProvider_EmptyLocations(t *testing.T) {
	_, err := NewContentProvider([]domain.ContentLocation{}, "/tmp")
	if err == nil {
		t.Fatal("Expected error for empty locations")
	}
	if !strings.Contains(err.Error(), "at least one") {
		t.Errorf("Error should mention 'at least one': %v", err)
	}
}

func TestNewContentProvider_PathIsFile(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "not-a-dir")
	if err := os.WriteFile(filePath, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: filePath},
	}

	_, err := NewContentProvider(locations, tempDir)
	if err == nil {
		t.Fatal("Expected error when path is a file, not directory")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("Error should mention 'not a directory': %v", err)
	}
}

func TestNewContentProvider_ResourcesIsFile(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	if err := os.MkdirAll(loc1, 0755); err != nil {
		t.Fatal(err)
	}
	// Create mcp-resources as a file, not directory
	resourcesFile := filepath.Join(loc1, "mcp-resources")
	if err := os.WriteFile(resourcesFile, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
	}

	_, err := NewContentProvider(locations, tempDir)
	if err == nil {
		t.Fatal("Expected error when mcp-resources is a file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("Error should mention 'not a directory': %v", err)
	}
}

func TestNewContentProvider_DuplicateResolvedPaths(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	createContentLocation(t, loc1, false)

	// Create a symlink pointing to the same directory
	symlinkPath := filepath.Join(tempDir, "docs-link")
	if err := os.Symlink(loc1, symlinkPath); err != nil {
		t.Skip("Symlinks not supported on this system")
	}

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
		{Name: "docs-link", Description: "Link to docs", Path: symlinkPath},
	}

	// Should succeed but log a warning (we can't easily test the warning)
	p, err := NewContentProvider(locations, tempDir)
	if err != nil {
		t.Fatalf("NewContentProvider should succeed with duplicate paths: %v", err)
	}

	// Both locations should be present
	resourceLocs := p.ResourceLocations()
	if len(resourceLocs) != 2 {
		t.Errorf("Expected 2 resource locations, got %d", len(resourceLocs))
	}
}

// --- Tests for LoadText, LoadYAML, LoadMarkdownWithFrontmatter ---

func TestContentProvider_LoadText(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	createContentLocation(t, loc1, false)

	filePath := filepath.Join(loc1, "mcp-resources", "test.txt")
	if err := os.WriteFile(filePath, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
	}
	p, _ := NewContentProvider(locations, tempDir)

	content, err := p.LoadText(filePath)
	if err != nil {
		t.Fatalf("LoadText failed: %v", err)
	}
	if content != "hello world" {
		t.Errorf("Expected 'hello world', got '%s'", content)
	}
}

func TestContentProvider_LoadText_Error(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	createContentLocation(t, loc1, false)

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
	}
	p, _ := NewContentProvider(locations, tempDir)

	_, err := p.LoadText("/non/existent/file.txt")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}

func TestContentProvider_LoadYAML(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	createContentLocation(t, loc1, false)

	filePath := filepath.Join(loc1, "test.yaml")
	if err := os.WriteFile(filePath, []byte("key: value"), 0644); err != nil {
		t.Fatal(err)
	}

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
	}
	p, _ := NewContentProvider(locations, tempDir)

	data, err := p.LoadYAML(filePath)
	if err != nil {
		t.Fatalf("LoadYAML failed: %v", err)
	}
	if data["key"] != "value" {
		t.Errorf("Expected 'value', got '%v'", data["key"])
	}
}

func TestContentProvider_LoadYAML_Errors(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	createContentLocation(t, loc1, false)

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
	}
	p, _ := NewContentProvider(locations, tempDir)

	// File not found
	_, err := p.LoadYAML("/non/existent.yaml")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}

	// Invalid YAML
	invalidPath := filepath.Join(loc1, "invalid.yaml")
	if err := os.WriteFile(invalidPath, []byte("invalid: : yaml"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = p.LoadYAML(invalidPath)
	if err == nil {
		t.Error("Expected error for invalid yaml")
	}
}

func TestContentProvider_LoadMarkdownWithFrontmatter(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	createContentLocation(t, loc1, false)

	filePath := filepath.Join(loc1, "test.md")
	content := "---\nname: Test\n---\nMarkdown content"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
	}
	p, _ := NewContentProvider(locations, tempDir)

	md, err := p.LoadMarkdownWithFrontmatter(filePath)
	if err != nil {
		t.Fatalf("LoadMarkdownWithFrontmatter failed: %v", err)
	}

	if md.Metadata["name"] != "Test" {
		t.Errorf("Expected metadata name 'Test', got '%v'", md.Metadata["name"])
	}
	if md.Content != "Markdown content" {
		t.Errorf("Expected content 'Markdown content', got '%s'", md.Content)
	}
}

func TestContentProvider_LoadMarkdownWithFrontmatter_CRLF(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	createContentLocation(t, loc1, false)

	filePath := filepath.Join(loc1, "test_crlf.md")
	content := "---\r\nname: Test\r\n---\r\nMarkdown content"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
	}
	p, _ := NewContentProvider(locations, tempDir)

	md, err := p.LoadMarkdownWithFrontmatter(filePath)
	if err != nil {
		t.Fatalf("LoadMarkdownWithFrontmatter failed with CRLF: %v", err)
	}

	if md.Metadata["name"] != "Test" {
		t.Errorf("Expected metadata name 'Test', got '%v'", md.Metadata["name"])
	}
}

func TestContentProvider_LoadMarkdownWithFrontmatter_EmptyFrontmatter(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	createContentLocation(t, loc1, false)

	filePath := filepath.Join(loc1, "test_empty.md")
	content := "---\n---\nMarkdown content"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
	}
	p, _ := NewContentProvider(locations, tempDir)

	md, err := p.LoadMarkdownWithFrontmatter(filePath)
	if err != nil {
		t.Fatalf("LoadMarkdownWithFrontmatter failed: %v", err)
	}

	if len(md.Metadata) != 0 {
		t.Errorf("Expected empty metadata, got %v", md.Metadata)
	}
}

func TestContentProvider_LoadMarkdownWithFrontmatter_Errors(t *testing.T) {
	tempDir := t.TempDir()
	loc1 := filepath.Join(tempDir, "docs")
	createContentLocation(t, loc1, false)

	locations := []domain.ContentLocation{
		{Name: "docs", Description: "Documentation", Path: loc1},
	}
	p, _ := NewContentProvider(locations, tempDir)

	tests := []struct {
		name    string
		content string
	}{
		{"No frontmatter", "Title: Test"},
		{"Missing closing", "---\nTitle: Test"},
		{"Invalid YAML", "---\nkey: : val\n---\nContent"},
		{"Closing --- not on own line", "---\nkey: val\n---foo\nContent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := filepath.Join(loc1, tt.name+".md")
			if err := os.WriteFile(filePath, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}
			_, err := p.LoadMarkdownWithFrontmatter(filePath)
			if err == nil {
				t.Errorf("Expected error for %s", tt.name)
			}
		})
	}
}
