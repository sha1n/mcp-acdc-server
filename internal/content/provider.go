package content

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MarkdownWithFrontmatter parsed markdown file with YAML frontmatter
type MarkdownWithFrontmatter struct {
	Metadata map[string]interface{}
	Content  string
}

// ContentProvider provider for loading content files
type ContentProvider struct {
	ContentDir   string
	ResourcesDir string
}

// NewContentProvider creates a new ContentProvider
func NewContentProvider(contentDir string) *ContentProvider {
	return &ContentProvider{
		ContentDir:   contentDir,
		ResourcesDir: filepath.Join(contentDir, "mcp-resources"),
	}
}

// GetPath returns a path within the content directory
func (p *ContentProvider) GetPath(parts ...string) string {
	allParts := append([]string{p.ContentDir}, parts...)
	return filepath.Join(allParts...)
}

// LoadText loads a text file
func (p *ContentProvider) LoadText(filePath string) (string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// LoadYAML loads and parses a YAML file
func (p *ContentProvider) LoadYAML(filePath string) (map[string]interface{}, error) {
	content, err := p.LoadText(filePath)
	if err != nil {
		return nil, err
	}

	var data map[string]interface{}
	if err := yaml.Unmarshal([]byte(content), &data); err != nil {
		return nil, fmt.Errorf("invalid YAML in %s: %w", filePath, err)
	}

	return data, nil
}

// LoadMarkdownWithFrontmatter loads a markdown file with YAML frontmatter
func (p *ContentProvider) LoadMarkdownWithFrontmatter(filePath string) (*MarkdownWithFrontmatter, error) {
	content, err := p.LoadText(filePath)
	if err != nil {
		return nil, err
	}

	// Normalize CRLF to LF to simplify parsing
	normalized := strings.ReplaceAll(content, "\r\n", "\n")

	if !strings.HasPrefix(normalized, "---\n") {
		return nil, fmt.Errorf("file must start with YAML frontmatter (---\\n) in %s", filePath)
	}

	const startDelimiterLen = 4
	remainder := normalized[startDelimiterLen:]

	// Check if we have an empty frontmatter (immediately closing)
	if strings.HasPrefix(remainder, "---\n") {
		// Empty metadata
		markdownContent := remainder[4:]
		return &MarkdownWithFrontmatter{
			Metadata: map[string]interface{}{},
			Content:  markdownContent,
		}, nil
	}

	// Find the closing --- on its own line
	// We search for "\n---"
	endIndex := strings.Index(remainder, "\n---")
	if endIndex == -1 {
		return nil, fmt.Errorf("invalid frontmatter format - missing closing --- in %s", filePath)
	}

	// Verify it's a valid closing delimiter (followed by newline or EOF)
	// endIndex points to the newline before ---
	// So we check remainder[endIndex+4] (length of "\n---" is 4)

	// Check if it is followed by newline or is end of file
	afterDelimiter := endIndex + 4
	if afterDelimiter < len(remainder) && remainder[afterDelimiter] != '\n' {
		// This might be something like "\n---foo", which is not a delimiter
		// In a real robust parser we would loop to find the next one, but for now let's assume valid markdown.
		// Or we can try to find the next one.
		// For simplicity, let's treat it as error or recurse.
		// But given the scope, let's just fail if it's not a proper delimiter.
		// Actually, standard says delimiter must be on its own line.
		return nil, fmt.Errorf("closing --- must be on its own line in %s", filePath)
	}

	frontmatterText := remainder[:endIndex]

	var contentStartIndex int
	if afterDelimiter < len(remainder) {
		// skip the following newline
		contentStartIndex = afterDelimiter + 1
	} else {
		contentStartIndex = len(remainder)
	}

	markdownContent := ""
	if contentStartIndex < len(remainder) {
		markdownContent = remainder[contentStartIndex:]
	}

	var metadata map[string]interface{}
	if err := yaml.Unmarshal([]byte(frontmatterText), &metadata); err != nil {
		return nil, fmt.Errorf("invalid YAML in frontmatter of %s: %w", filePath, err)
	}

	return &MarkdownWithFrontmatter{
		Metadata: metadata,
		Content:  markdownContent,
	}, nil
}
