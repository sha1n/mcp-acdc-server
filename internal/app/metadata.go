package app

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"gopkg.in/yaml.v3"
)

// defaultVersion is substituted when the build-injected version is empty.
const defaultVersion = "0.0.0"

// defaultRepoBaseName is substituted when a content directory's base name cannot be derived.
const defaultRepoBaseName = "Repository"

// resolvedMetadata carries the effective server metadata and how it was obtained.
type resolvedMetadata struct {
	metadata  domain.McpMetadata
	defaulted bool
}

// resolveMetadata loads .acdc/config.yaml from the content root, falling back to
// built-in defaults when the manifest is absent. A manifest that exists but cannot
// be read (permissions, is-a-directory, etc.) is treated as misconfiguration and
// returned as an error rather than silently defaulted.
//
// The content directory itself is validated first: a missing .acdc/config.yaml
// under a nonexistent or non-directory --content-dir also satisfies
// fs.ErrNotExist, which would otherwise be misread as "no manifest" and
// silently defaulted, only to fail one step later during content root
// resolution with no mention of the actual misconfiguration.
func resolveMetadata(cp *content.ContentProvider, version string) (resolvedMetadata, error) {
	if info, statErr := os.Stat(cp.ContentDir); statErr != nil || !info.IsDir() {
		return resolvedMetadata{}, fmt.Errorf("failed to resolve content directory %s: not found or not a directory", cp.ContentDir)
	}

	metadataPath := cp.ConfigFile

	mdBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			metadata := domain.DefaultMetadata(repoBaseName(cp.ContentDir), resolveVersion(version))
			slog.Info(".acdc/config.yaml not found, using built-in defaults",
				"server_name", metadata.Server.Name,
				"index_include", metadata.Index.Include,
			)
			return resolvedMetadata{metadata: metadata, defaulted: true}, nil
		}
		return resolvedMetadata{}, fmt.Errorf("failed to read metadata file: %w", err)
	}

	var metadata domain.McpMetadata
	if err := yaml.Unmarshal(mdBytes, &metadata); err != nil {
		return resolvedMetadata{}, fmt.Errorf("failed to parse metadata: %w", err)
	}

	if err := metadata.Validate(); err != nil {
		return resolvedMetadata{}, fmt.Errorf("metadata validation failed: %w", err)
	}

	return resolvedMetadata{metadata: metadata, defaulted: false}, nil
}

// repoBaseName derives a display-friendly repository name from the content directory,
// falling back to a generic name when no meaningful base name can be derived.
func repoBaseName(contentDir string) string {
	abs, err := filepath.Abs(contentDir)
	if err != nil {
		abs = contentDir
	}

	base := filepath.Base(abs)
	if base == "" || base == "." || base == ".." || base == string(filepath.Separator) {
		return defaultRepoBaseName
	}
	return base
}

// resolveVersion returns the build-injected version, or a placeholder when it is empty.
func resolveVersion(version string) string {
	if version == "" {
		return defaultVersion
	}
	return version
}
