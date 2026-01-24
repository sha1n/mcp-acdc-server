package resources

import (
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/content"
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

	c, err := content.NewContentProvider("").LoadMarkdownWithFrontmatter(defn.FilePath)
	if err != nil {
		return "", err
	}
	return c.Content, nil
}

// GetAllResourceContents retrieves contents for all resources
func (p *ResourceProvider) GetAllResourceContents() []map[string]string {
	var results []map[string]string
	for _, defn := range p.definitions {
		content, err := p.ReadResource(defn.URI)
		if err != nil {
			slog.Error("Error reading resource for indexing", "uri", defn.URI, "error", err)
			continue
		}
		results = append(results, map[string]string{
			FieldURI:      defn.URI,
			FieldName:     defn.Name,
			FieldContent:  content,
			FieldKeywords: strings.Join(defn.Keywords, ","),
		})
	}
	return results
}

// DiscoverResources discovers resources from markdown files
func DiscoverResources(cp *content.ContentProvider) ([]ResourceDefinition, error) {
	var definitions []ResourceDefinition
	resourcesDir := cp.ResourcesDir

	err := filepath.WalkDir(resourcesDir, func(path string, d fs.DirEntry, err error) error {
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

		// Derive URI
		relPath, err := filepath.Rel(resourcesDir, path)
		if err != nil {
			return err
		}

		relPathNoExt := strings.TrimSuffix(relPath, filepath.Ext(relPath))
		// normalized for URI (slashes)
		uriPath := filepath.ToSlash(relPathNoExt)
		uri := fmt.Sprintf("acdc://%s", uriPath)

		definitions = append(definitions, ResourceDefinition{
			URI:         uri,
			Name:        name,
			Description: description,
			MIMEType:    "text/markdown",
			FilePath:    path,
			Keywords:    keywords,
		})

		slog.Info("Loaded resource", "uri", uri, "name", name)

		return nil
	})

	if err != nil {
		return nil, err
	}

	return definitions, nil
}
