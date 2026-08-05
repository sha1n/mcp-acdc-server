package content

import (
	"errors"
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

// Layout names for the ACDC content convention. Every path ACDC resolves inside
// a content root composes from these, so relocating the layout is a change to
// this block alone.
const (
	ConfigDirName    = ".acdc"
	ConfigFileName   = "config.yaml"
	PromptsDirName   = "prompts"
	ResourcesDirName = "resources"
)

// ContentProvider provider for loading content files
type ContentProvider struct {
	ContentDir   string
	ConfigDir    string
	ConfigFile   string
	ResourcesDir string
	PromptsDir   string
}

// NewContentProvider creates a new ContentProvider
func NewContentProvider(contentDir string) *ContentProvider {
	configDir := filepath.Join(contentDir, ConfigDirName)
	return &ContentProvider{
		ContentDir:   contentDir,
		ConfigDir:    configDir,
		ConfigFile:   filepath.Join(configDir, ConfigFileName),
		ResourcesDir: filepath.Join(configDir, ResourcesDirName),
		PromptsDir:   filepath.Join(configDir, PromptsDirName),
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
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	parsed, err := ParseMarkdown(raw, FrontmatterRequired)
	if err != nil {
		if yamlErr := errors.Unwrap(err); yamlErr != nil && strings.HasPrefix(err.Error(), "invalid YAML in frontmatter: ") {
			return nil, fmt.Errorf("invalid YAML in frontmatter of %s: %w", filePath, yamlErr)
		}
		return nil, fmt.Errorf("%w in %s", err, filePath)
	}

	return &MarkdownWithFrontmatter{
		Metadata: parsed.Metadata,
		Content:  string(parsed.Body),
	}, nil
}
