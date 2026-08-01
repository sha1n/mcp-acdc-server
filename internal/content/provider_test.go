package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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

func TestParseMarkdown_OptionalFrontmatterAndDerivedMetadata(t *testing.T) {
	raw := []byte("# OAuth Setup\n\nUse client credentials securely.\n")
	parsed, err := ParseMarkdown(raw, FrontmatterOptional)
	require.NoError(t, err)

	doc, err := BuildSourceDocument(parsed, SourceOptions{
		URI:          "acdc://docs/platform/oauth",
		FilePath:     "/repo/docs/platform/oauth.md",
		RelativePath: "docs/platform/oauth.md",
		Raw:          raw,
	})
	require.NoError(t, err)
	require.Equal(t, "OAuth Setup", doc.Name)
	require.Equal(t, "Use client credentials securely.", doc.Description)
	require.Equal(t, []string{"docs", "platform", "oauth"}, doc.PathLabels)
	require.Equal(t, 1, doc.BodyStartLine)
	require.Len(t, doc.Fingerprint, 64)
}

func TestParseMarkdown_AuthoredMetadataPrecedence(t *testing.T) {
	raw := []byte("---\nname: Auth Guide\ntitle: Ignored\ndescription: Auth details\nkeywords: [oauth, tokens]\n---\n# Other Heading\nBody\n")
	parsed, err := ParseMarkdown(raw, FrontmatterOptional)
	require.NoError(t, err)
	doc, err := BuildSourceDocument(parsed, SourceOptions{URI: "acdc://auth", RelativePath: "docs/auth.md", Raw: raw})
	require.NoError(t, err)
	require.Equal(t, "Auth Guide", doc.Name)
	require.Equal(t, "Auth details", doc.Description)
	require.Equal(t, []string{"oauth", "tokens"}, doc.Keywords)
	require.Equal(t, 7, doc.BodyStartLine)
}

func TestParseMarkdown_FrontmatterModes(t *testing.T) {
	_, err := ParseMarkdown([]byte("# Plain"), FrontmatterRequired)
	require.ErrorContains(t, err, "file must start with YAML frontmatter")
	_, err = ParseMarkdown([]byte("---\ninvalid: : yaml\n---\nBody"), FrontmatterOptional)
	require.ErrorContains(t, err, "invalid YAML in frontmatter")
}

func TestParseMarkdown_FrontmatterBoundaries(t *testing.T) {
	tests := []struct {
		name          string
		raw           string
		wantName      any
		wantBody      string
		wantStartLine int
	}{
		{name: "empty at EOF", raw: "---\n---", wantBody: "", wantStartLine: 3},
		{name: "metadata at EOF", raw: "---\nname: Guide\n---", wantName: "Guide", wantBody: "", wantStartLine: 3},
		{name: "null metadata", raw: "---\nnull\n---\nBody", wantBody: "Body", wantStartLine: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := ParseMarkdown([]byte(tt.raw), FrontmatterOptional)
			require.NoError(t, err)
			require.Equal(t, tt.wantName, parsed.Metadata["name"])
			require.Equal(t, tt.wantBody, string(parsed.Body))
			require.Equal(t, tt.wantStartLine, parsed.BodyStartLine)
		})
	}
}

func TestBuildSourceDocument_NilParsedMarkdown(t *testing.T) {
	_, err := BuildSourceDocument(nil, SourceOptions{})
	require.EqualError(t, err, "parsed markdown is required")
}

func TestBuildSourceDocument_NonStringMetadataFallsBack(t *testing.T) {
	raw := []byte("---\nname: 42\ndescription: [invalid]\n---\n# Derived Name\n\nDerived description.\n")
	parsed, err := ParseMarkdown(raw, FrontmatterOptional)
	require.NoError(t, err)

	doc, err := BuildSourceDocument(parsed, SourceOptions{
		URI:          "acdc://docs/client-credentials",
		RelativePath: "Docs/clientCredentials/oauth_oauth.md",
		Raw:          raw,
	})
	require.NoError(t, err)
	require.Equal(t, "Derived Name", doc.Name)
	require.Equal(t, "Derived description.", doc.Description)
	require.Equal(t, []string{"docs", "client", "credentials", "oauth"}, doc.PathLabels)
}

func TestBuildSourceDocument_DescriptionRuneLimit(t *testing.T) {
	parsed := &ParsedMarkdown{Metadata: map[string]any{}}
	longDescription := strings.Repeat("é", 201)
	parsed.Metadata["description"] = longDescription
	doc, err := BuildSourceDocument(parsed, SourceOptions{})
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("é", 200), doc.Description)
	require.Len(t, []rune(doc.Description), 200)
}

func TestBuildSourceDocument_MissingASTMetadataIsEmpty(t *testing.T) {
	doc, err := BuildSourceDocument(&ParsedMarkdown{Metadata: map[string]any{}}, SourceOptions{})
	require.NoError(t, err)
	require.Empty(t, doc.Name)
	require.Empty(t, doc.Description)
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

func TestContentProvider_LoadMarkdownWithFrontmatter_ReadError(t *testing.T) {
	p := NewContentProvider(t.TempDir())
	_, err := p.LoadMarkdownWithFrontmatter("missing.md")
	require.Error(t, err)
}

func TestContentProvider_GetPath(t *testing.T) {
	p := NewContentProvider("/root")
	path := p.GetPath("subdir", "file.txt")
	expected := "/root/subdir/file.txt"
	if path != expected {
		t.Errorf("Expected '%s', got '%s'", expected, path)
	}
}
