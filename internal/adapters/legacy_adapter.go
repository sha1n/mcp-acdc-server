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
	// LegacyAdapterName is the identifier for the legacy adapter
	LegacyAdapterName = "legacy"

	// LegacyResourcesDir is the directory name for resources in legacy structure
	LegacyResourcesDir = "mcp-resources"

	// LegacyPromptsDir is the directory name for prompts in legacy structure
	LegacyPromptsDir = "mcp-prompts"
)

// LegacyAdapter implements the legacy ACDC directory structure adapter.
// It expects content locations to have:
//   - mcp-resources/ directory (required) - contains markdown resources
//   - mcp-prompts/ directory (optional) - contains prompt templates
//
// DEPRECATED: This adapter is provided for backward compatibility only.
// New content locations should use the ACDC native adapter (resources/prompts).
type LegacyAdapter struct{}

// NewLegacyAdapter creates a new legacy adapter
func NewLegacyAdapter() *LegacyAdapter {
	return &LegacyAdapter{}
}

// Name returns the adapter identifier
func (a *LegacyAdapter) Name() string {
	return LegacyAdapterName
}

// CanHandle checks if the path contains a legacy structure
func (a *LegacyAdapter) CanHandle(basePath string) bool {
	resourcePath := filepath.Join(basePath, LegacyResourcesDir)
	info, err := os.Stat(resourcePath)
	return err == nil && info.IsDir()
}

// DiscoverResources discovers resources from the mcp-resources/ directory
func (a *LegacyAdapter) DiscoverResources(location Location, cp *content.ContentProvider) ([]resources.ResourceDefinition, error) {
	// Log deprecation warning
	slog.Warn("Using legacy adapter - consider migrating to native structure",
		"location", location.Name,
		"legacy_dir", LegacyResourcesDir,
		"new_dir", ACDCResourcesDir,
		"adapter", LegacyAdapterName)

	resourcePath := filepath.Join(location.BasePath, LegacyResourcesDir)

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
			slog.Warn("Skipping invalid resource file", "file", d.Name(), "error", err, "adapter", LegacyAdapterName)
			return nil
		}

		// Extract metadata
		name, _ := md.Metadata["name"].(string)
		description, _ := md.Metadata["description"].(string)

		if name == "" || description == "" {
			slog.Warn("Skipping resource with missing metadata", "file", d.Name(), "adapter", LegacyAdapterName)
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

		slog.Info("Loaded resource", "uri", uri, "name", name, "source", location.Name, "adapter", LegacyAdapterName)

		return nil
	})

	if err != nil {
		return nil, err
	}

	return definitions, nil
}

// DiscoverPrompts discovers prompts from the mcp-prompts/ directory
func (a *LegacyAdapter) DiscoverPrompts(location Location, cp *content.ContentProvider) ([]prompts.PromptDefinition, error) {
	// Log deprecation warning
	slog.Warn("Using legacy adapter - consider migrating to native structure",
		"location", location.Name,
		"legacy_dir", LegacyPromptsDir,
		"new_dir", ACDCPromptsDir,
		"adapter", LegacyAdapterName)

	promptPath := filepath.Join(location.BasePath, LegacyPromptsDir)

	// Check if prompts directory exists (it's optional)
	info, err := os.Stat(promptPath)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Debug("Prompts directory not found (optional)", "path", promptPath, "adapter", LegacyAdapterName)
			return []prompts.PromptDefinition{}, nil
		}
		return nil, fmt.Errorf("error checking prompts directory: %w", err)
	}
	if !info.IsDir() {
		slog.Warn("Prompts path is not a directory", "path", promptPath, "adapter", LegacyAdapterName)
		return []prompts.PromptDefinition{}, nil
	}

	var definitions []prompts.PromptDefinition

	err = filepath.WalkDir(promptPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Error("Error walking prompts directory", "path", path, "error", err, "adapter", LegacyAdapterName)
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
			slog.Warn("Skipping invalid prompt file", "file", d.Name(), "error", err, "adapter", LegacyAdapterName)
			return nil
		}

		// Extract metadata
		baseName, _ := md.Metadata["name"].(string)
		description, _ := md.Metadata["description"].(string)

		if baseName == "" || description == "" {
			slog.Warn("Skipping prompt with missing metadata", "file", d.Name(), "adapter", LegacyAdapterName)
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
			slog.Warn("Skipping prompt with invalid template", "file", d.Name(), "error", err, "adapter", LegacyAdapterName)
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

		slog.Info("Loaded prompt", "name", namespacedName, "source", location.Name, "adapter", LegacyAdapterName)

		return nil
	})

	return definitions, err
}
