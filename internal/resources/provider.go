package resources

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
)

// ResourceProvider provides access to resources
type ResourceProvider struct {
	definitions []ResourceDefinition
	uriMap      map[string]ResourceDefinition
}

// NewResourceProvider creates a new resource provider
func NewResourceProvider(definitions []ResourceDefinition) *ResourceProvider {
	uriMap := make(map[string]ResourceDefinition)
	for _, d := range definitions {
		uriMap[d.URI] = d
	}
	return &ResourceProvider{
		definitions: definitions,
		uriMap:      uriMap,
	}
}

// ListResources lists all available resources
func (p *ResourceProvider) ListResources() []mcp.Resource {
	resources := make([]mcp.Resource, len(p.definitions))
	for i, d := range p.definitions {
		resources[i] = mcp.Resource{
			URI:         d.URI,
			Name:        d.Name,
			Description: d.Description,
			MIMEType:    d.MIMEType,
		}
	}
	return resources
}

// ReadResource reads a resource by URI
func (p *ResourceProvider) ReadResource(uri string) (string, error) {
	defn, ok := p.uriMap[uri]
	if !ok {
		return "", fmt.Errorf("unknown resource: %s", uri)
	}

	// Read the file directly
	data, err := os.ReadFile(defn.FilePath)
	if err != nil {
		return "", err
	}

	// Parse frontmatter to extract content only
	contentStr := string(data)
	normalized := strings.ReplaceAll(contentStr, "\r\n", "\n")

	if !strings.HasPrefix(normalized, "---\n") {
		return contentStr, nil // No frontmatter, return as-is
	}

	// Find closing delimiter
	remainder := normalized[4:]
	if strings.HasPrefix(remainder, "---\n") {
		return remainder[4:], nil // Empty frontmatter
	}

	endIndex := strings.Index(remainder, "\n---")
	if endIndex == -1 {
		return contentStr, nil // Malformed, return as-is
	}

	afterDelimiter := endIndex + 4
	if afterDelimiter < len(remainder) && remainder[afterDelimiter] == '\n' {
		return remainder[afterDelimiter+1:], nil
	} else if afterDelimiter >= len(remainder) {
		return "", nil
	}

	return contentStr, nil
}

// StreamResources streams all resource contents to a channel
func (p *ResourceProvider) StreamResources(ctx context.Context, ch chan<- domain.Document) error {
	for _, defn := range p.definitions {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resourceContent, err := p.ReadResource(defn.URI)
		if err != nil {
			slog.Error("Error reading resource for indexing", "uri", defn.URI, "error", err)
			continue
		}

		doc := domain.Document{
			URI:      defn.URI,
			Name:     defn.Name,
			Content:  resourceContent,
			Keywords: defn.Keywords,
			Source:   defn.Source,
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case ch <- doc:
		}
	}
	return nil
}

// DiscoverResources discovers resources from markdown files in multiple locations
func DiscoverResources(locations []content.ResourceLocation, cp *content.ContentProvider) ([]ResourceDefinition, error) {
	var definitions []ResourceDefinition

	for _, loc := range locations {
		locDefs, err := discoverResourcesInLocation(loc, cp)
		if err != nil {
			return nil, fmt.Errorf("error discovering resources in %s: %w", loc.Name, err)
		}
		definitions = append(definitions, locDefs...)
	}

	return definitions, nil
}

// discoverResourcesInLocation discovers resources in a single location
func discoverResourcesInLocation(loc content.ResourceLocation, cp *content.ContentProvider) ([]ResourceDefinition, error) {
	var definitions []ResourceDefinition

	err := filepath.WalkDir(loc.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}

		// Parse frontmatter
		md, err := cp.LoadMarkdownWithFrontmatter(path)
		if err != nil {
			slog.Warn("Skipping invalid resource file", "file", d.Name(), "error", err)
			return nil
		}

		// Extract metadata
		name, _ := md.Metadata["name"].(string)
		description, _ := md.Metadata["description"].(string)

		if name == "" || description == "" {
			slog.Warn("Skipping resource with missing metadata", "file", d.Name())
			return nil
		}

		// Extract optional keywords
		var keywords []string
		if kw, ok := md.Metadata["keywords"].([]interface{}); ok {
			for _, k := range kw {
				if s, ok := k.(string); ok {
					keywords = append(keywords, s)
				}
			}
		}

		// Derive URI with source prefix
		relPath, err := filepath.Rel(loc.Path, path)
		if err != nil {
			return err
		}

		relPathNoExt := strings.TrimSuffix(relPath, filepath.Ext(relPath))
		// normalized for URI (slashes)
		uriPath := filepath.ToSlash(relPathNoExt)
		uri := fmt.Sprintf("acdc://%s/%s", loc.Name, uriPath)

		definitions = append(definitions, ResourceDefinition{
			URI:         uri,
			Name:        name,
			Description: description,
			MIMEType:    "text/markdown",
			FilePath:    path,
			Keywords:    keywords,
			Source:      loc.Name,
		})

		slog.Info("Loaded resource", "uri", uri, "name", name, "source", loc.Name)

		return nil
	})

	if err != nil {
		return nil, err
	}

	return definitions, nil
}
