package content

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContentProvider_LoadText(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.txt")
	err := os.WriteFile(filePath, []byte("hello world"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	p := NewContentProvider(tempDir)
	content, err := p.LoadText(filePath)
	if err != nil {
		t.Fatalf("LoadText failed: %v", err)
	}
	if content != "hello world" {
		t.Errorf("Expected 'hello world', got '%s'", content)
	}
}

func TestContentProvider_LoadYAML(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.yaml")
	err := os.WriteFile(filePath, []byte("key: value"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	p := NewContentProvider(tempDir)
	data, err := p.LoadYAML(filePath)
	if err != nil {
		t.Fatalf("LoadYAML failed: %v", err)
	}
	if data["key"] != "value" {
		t.Errorf("Expected value 'value', got '%v'", data["key"])
	}
}

func TestContentProvider_LoadMarkdownWithFrontmatter(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.md")
	content := "---\nname: Test\n---\nMarkdown content"
	err := os.WriteFile(filePath, []byte(content), 0644)
	if err != nil {
		t.Fatal(err)
	}

	p := NewContentProvider(tempDir)
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
	filePath := filepath.Join(tempDir, "test_crlf.md")
	content := "---\r\nname: Test\r\n---\r\nMarkdown content"
	err := os.WriteFile(filePath, []byte(content), 0644)
	if err != nil {
		t.Fatal(err)
	}

	p := NewContentProvider(tempDir)
	md, err := p.LoadMarkdownWithFrontmatter(filePath)
	if err != nil {
		t.Fatalf("LoadMarkdownWithFrontmatter failed with CRLF: %v", err)
	}

	if md.Metadata["name"] != "Test" {
		t.Errorf("Expected metadata name 'Test', got '%v'", md.Metadata["name"])
	}
	if md.Content != "Markdown content" {
		t.Errorf("Expected content 'Markdown content', got '%s'", md.Content)
	}
}

func TestContentProvider_LoadMarkdownWithFrontmatter_EmptyFrontmatter(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test_empty.md")
	content := "---\n---\nMarkdown content"
	err := os.WriteFile(filePath, []byte(content), 0644)
	if err != nil {
		t.Fatal(err)
	}

	p := NewContentProvider(tempDir)
	md, err := p.LoadMarkdownWithFrontmatter(filePath)
	if err != nil {
		t.Fatalf("LoadMarkdownWithFrontmatter failed with empty frontmatter: %v", err)
	}

	if len(md.Metadata) != 0 {
		t.Errorf("Expected empty metadata, got %v", md.Metadata)
	}
	if md.Content != "Markdown content" {
		t.Errorf("Expected content 'Markdown content', got '%s'", md.Content)
	}
}

func TestContentProvider_LoadText_Error(t *testing.T) {
	p := NewContentProvider(t.TempDir())
	_, err := p.LoadText("non-existent.txt")
	if err == nil {
		t.Error("Expected error for non-existent file, got nil")
	}
}

func TestContentProvider_LoadYAML_Error(t *testing.T) {
	tempDir := t.TempDir()
	p := NewContentProvider(tempDir)

	// Case 1: File not found
	_, err := p.LoadYAML("non-existent.yaml")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}

	// Case 2: Invalid YAML
	filePath := filepath.Join(tempDir, "invalid.yaml")
	_ = os.WriteFile(filePath, []byte("invalid: : yaml"), 0644)
	_, err = p.LoadYAML(filePath)
	if err == nil {
		t.Error("Expected error for invalid yaml")
	}
}

func TestContentProvider_LoadMarkdownWithFrontmatter_Errors(t *testing.T) {
	tempDir := t.TempDir()
	p := NewContentProvider(tempDir)

	tests := []struct {
		name    string
		content string
	}{
		{"No frontmatter", "Title: Test"},
		{"Missing closing", "---\nTitle: Test"},
		{"Invalid YAML", "---\nkey: : val\n---\nContent"},
		{"Closing --- not on own line", "---\nkey: val\n---foo\nContent"},
		{"Closing --- followed by text", "---\nkey: val\n--- text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := filepath.Join(tempDir, tt.name+".md")
			_ = os.WriteFile(filePath, []byte(tt.content), 0644)
			_, err := p.LoadMarkdownWithFrontmatter(filePath)
			if err == nil {
				t.Errorf("Expected error for %s", tt.name)
			}
		})
	}
}

func TestContentProvider_GetPath(t *testing.T) {
	p := NewContentProvider("/root")
	path := p.GetPath("subdir", "file.txt")
	expected := "/root/subdir/file.txt"
	if path != expected {
		t.Errorf("Expected '%s', got '%s'", expected, path)
	}
}
