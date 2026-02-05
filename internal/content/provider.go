package content

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"gopkg.in/yaml.v3"
)

// MarkdownWithFrontmatter parsed markdown file with YAML frontmatter
type MarkdownWithFrontmatter struct {
	Metadata map[string]interface{}
	Content  string
}

// ResourceLocation represents a named resource directory
type ResourceLocation struct {
	Name string // Content location name
	Path string // Full path to mcp-resources/ directory
}

// PromptLocation represents a named prompt directory
type PromptLocation struct {
	Name string // Content location name
	Path string // Full path to mcp-prompts/ directory
}

// ContentProvider provider for loading content files from multiple locations
type ContentProvider struct {
	locations []resolvedLocation
}

// resolvedLocation is an internal type with resolved absolute paths
type resolvedLocation struct {
	name         string
	basePath     string // Resolved absolute path to the content location
	resourcePath string // Resolved absolute path to mcp-resources/
	promptPath   string // Resolved absolute path to mcp-prompts/ (may not exist)
	hasPrompts   bool   // Whether mcp-prompts/ exists
}

// NewContentProvider creates a new ContentProvider with multiple content locations.
// Paths in locations can be absolute or relative to configDir.
// Returns an error if any path doesn't exist or lacks an mcp-resources/ directory.
func NewContentProvider(locations []domain.ContentLocation, configDir string) (*ContentProvider, error) {
	if len(locations) == 0 {
		return nil, fmt.Errorf("at least one content location is required")
	}

	resolved := make([]resolvedLocation, 0, len(locations))
	seenPaths := make(map[string]string) // resolved path -> location name (for duplicate detection)

	for _, loc := range locations {
		// Resolve the path
		basePath := loc.Path
		if !filepath.IsAbs(basePath) {
			basePath = filepath.Join(configDir, basePath)
		}

		// Clean and resolve the path
		basePath = filepath.Clean(basePath)

		// Resolve symlinks for duplicate detection
		resolvedBasePath, err := filepath.EvalSymlinks(basePath)
		if err != nil {
			// If EvalSymlinks fails, the path likely doesn't exist
			return nil, fmt.Errorf("content location %q: path does not exist: %s", loc.Name, basePath)
		}

		// Check for duplicate resolved paths
		if existingName, exists := seenPaths[resolvedBasePath]; exists {
			slog.Warn("Duplicate content path detected",
				"path", resolvedBasePath,
				"location1", existingName,
				"location2", loc.Name)
		}
		seenPaths[resolvedBasePath] = loc.Name

		// Verify the path exists and is a directory
		info, err := os.Stat(basePath)
		if err != nil {
			return nil, fmt.Errorf("content location %q: path does not exist: %s", loc.Name, basePath)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("content location %q: path is not a directory: %s", loc.Name, basePath)
		}

		// Check for mcp-resources/ directory (required)
		resourcePath := filepath.Join(basePath, "mcp-resources")
		resourceInfo, err := os.Stat(resourcePath)
		if err != nil {
			return nil, fmt.Errorf("content location %q: missing mcp-resources/ directory in %s", loc.Name, basePath)
		}
		if !resourceInfo.IsDir() {
			return nil, fmt.Errorf("content location %q: mcp-resources is not a directory in %s", loc.Name, basePath)
		}

		// Check for mcp-prompts/ directory (optional)
		promptPath := filepath.Join(basePath, "mcp-prompts")
		hasPrompts := false
		if promptInfo, err := os.Stat(promptPath); err == nil && promptInfo.IsDir() {
			hasPrompts = true
		}

		resolved = append(resolved, resolvedLocation{
			name:         loc.Name,
			basePath:     basePath,
			resourcePath: resourcePath,
			promptPath:   promptPath,
			hasPrompts:   hasPrompts,
		})
	}

	return &ContentProvider{
		locations: resolved,
	}, nil
}

// ResourceLocations returns all resource directories with their location names
func (p *ContentProvider) ResourceLocations() []ResourceLocation {
	result := make([]ResourceLocation, len(p.locations))
	for i, loc := range p.locations {
		result[i] = ResourceLocation{
			Name: loc.name,
			Path: loc.resourcePath,
		}
	}
	return result
}

// PromptLocations returns all prompt directories that exist, with their location names
func (p *ContentProvider) PromptLocations() []PromptLocation {
	result := make([]PromptLocation, 0)
	for _, loc := range p.locations {
		if loc.hasPrompts {
			result = append(result, PromptLocation{
				Name: loc.name,
				Path: loc.promptPath,
			})
		}
	}
	return result
}

// LoadText loads a text file from an absolute path
func (p *ContentProvider) LoadText(filePath string) (string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// LoadYAML loads and parses a YAML file from an absolute path
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
