package adapters

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/prompts"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
)

const (
	// ACDCAdapterName is the identifier for the ACDC native adapter
	ACDCAdapterName = "acdc-mcp"

	// ACDCResourcesDir is the directory name for resources in ACDC native structure
	ACDCResourcesDir = "resources"

	// ACDCPromptsDir is the directory name for prompts in ACDC native structure
	ACDCPromptsDir = "prompts"
)

// ACDCAdapter implements the native ACDC directory structure adapter.
// It expects content locations to have:
//   - resources/ directory (required) - contains markdown resources
//   - prompts/ directory (optional) - contains prompt templates
type ACDCAdapter struct{}

// NewACDCAdapter creates a new ACDC native adapter
func NewACDCAdapter() *ACDCAdapter {
	return &ACDCAdapter{}
}

// Name returns the adapter identifier
func (a *ACDCAdapter) Name() string {
	return ACDCAdapterName
}

// CanHandle checks if the path contains an ACDC native structure
func (a *ACDCAdapter) CanHandle(basePath string) bool {
	resourcePath := filepath.Join(basePath, ACDCResourcesDir)
	info, err := os.Stat(resourcePath)
	return err == nil && info.IsDir()
}

// DiscoverResources discovers resources from the resources/ directory
func (a *ACDCAdapter) DiscoverResources(location Location, cp *content.ContentProvider) ([]resources.ResourceDefinition, error) {
	resourcePath := filepath.Join(location.BasePath, ACDCResourcesDir)

	// Verify directory exists
	info, err := os.Stat(resourcePath)
	if err != nil {
		return nil, fmt.Errorf("resources directory not found: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("resources path is not a directory: %s", resourcePath)
	}

	var definitions []resources.ResourceDefinition

	err = filepath.WalkDir(resourcePath, func(path string, d fs.DirEntry, err error) error {
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
			slog.Warn("Skipping invalid resource file", "file", d.Name(), "error", err, "adapter", ACDCAdapterName)
			return nil
		}

		// Extract metadata
		name, _ := md.Metadata["name"].(string)
		description, _ := md.Metadata["description"].(string)

		if name == "" || description == "" {
			slog.Warn("Skipping resource with missing metadata", "file", d.Name(), "adapter", ACDCAdapterName)
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
		relPath, err := filepath.Rel(resourcePath, path)
		if err != nil {
			return err
		}

		relPathNoExt := strings.TrimSuffix(relPath, filepath.Ext(relPath))
		// normalized for URI (slashes)
		uriPath := filepath.ToSlash(relPathNoExt)
		uri := fmt.Sprintf("acdc://%s/%s", location.Name, uriPath)

		definitions = append(definitions, resources.ResourceDefinition{
			URI:         uri,
			Name:        name,
			Description: description,
			MIMEType:    "text/markdown",
			FilePath:    path,
			Keywords:    keywords,
			Source:      location.Name,
		})

		slog.Info("Loaded resource", "uri", uri, "name", name, "source", location.Name, "adapter", ACDCAdapterName)

		return nil
	})

	if err != nil {
		return nil, err
	}

	return definitions, nil
}

// DiscoverPrompts discovers prompts from the prompts/ directory
func (a *ACDCAdapter) DiscoverPrompts(location Location, cp *content.ContentProvider) ([]prompts.PromptDefinition, error) {
	promptPath := filepath.Join(location.BasePath, ACDCPromptsDir)

	// Check if prompts directory exists (it's optional)
	info, err := os.Stat(promptPath)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Debug("Prompts directory not found (optional)", "path", promptPath, "adapter", ACDCAdapterName)
			return []prompts.PromptDefinition{}, nil
		}
		return nil, fmt.Errorf("error checking prompts directory: %w", err)
	}
	if !info.IsDir() {
		slog.Warn("Prompts path is not a directory", "path", promptPath, "adapter", ACDCAdapterName)
		return []prompts.PromptDefinition{}, nil
	}

	var definitions []prompts.PromptDefinition

	err = filepath.WalkDir(promptPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Error("Error walking prompts directory", "path", path, "error", err, "adapter", ACDCAdapterName)
			return nil // continue walking
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
			slog.Warn("Skipping invalid prompt file", "file", d.Name(), "error", err, "adapter", ACDCAdapterName)
			return nil
		}

		// Extract metadata
		baseName, _ := md.Metadata["name"].(string)
		description, _ := md.Metadata["description"].(string)

		if baseName == "" || description == "" {
			slog.Warn("Skipping prompt with missing metadata", "file", d.Name(), "adapter", ACDCAdapterName)
			return nil
		}

		// Create namespaced name
		namespacedName := fmt.Sprintf("%s:%s", location.Name, baseName)

		// Extract arguments
		var arguments []prompts.PromptArgument
		if args, ok := md.Metadata["arguments"].([]interface{}); ok {
			for _, argItem := range args {
				if amap, ok := argItem.(map[string]interface{}); ok {
					argName, _ := amap["name"].(string)
					argDesc, _ := amap["description"].(string)
					argReq, ok := amap["required"].(bool)
					if !ok {
						argReq = true // default to required
					}
					if argName != "" {
						arguments = append(arguments, prompts.PromptArgument{
							Name:        argName,
							Description: argDesc,
							Required:    argReq,
						})
					}
				}
			}
		}

		// Parse and cache template
		tmpl, err := template.New(namespacedName).Option("missingkey=zero").Parse(md.Content)
		if err != nil {
			slog.Warn("Skipping prompt with invalid template", "file", d.Name(), "error", err, "adapter", ACDCAdapterName)
			return nil
		}

		definitions = append(definitions, prompts.PromptDefinition{
			Name:        namespacedName,
			Description: description,
			Arguments:   arguments,
			FilePath:    path,
			Template:    tmpl,
			Source:      location.Name,
		})

		slog.Info("Loaded prompt", "name", namespacedName, "source", location.Name, "adapter", ACDCAdapterName)

		return nil
	})

	return definitions, err
}
