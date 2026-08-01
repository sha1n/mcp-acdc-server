package resources

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
)

// DiscoveryResult is the immutable source and chunk catalog input.
type DiscoveryResult struct {
	Sources []domain.SourceDocument
	Chunks  []domain.Chunk
}

// Discover loads configured Markdown sources, or legacy mcp-resources when
// index is nil.
func Discover(ctx context.Context, cp *content.ContentProvider, index *domain.IndexMetadata, scheme string) (DiscoveryResult, error) {
	if index == nil {
		return discoverLegacy(ctx, cp, scheme)
	}

	patterns, err := validatePatterns(index.Include)
	if err != nil {
		return DiscoveryResult{}, err
	}
	excludes, err := validatePatterns(index.Exclude)
	if err != nil {
		return DiscoveryResult{}, err
	}

	root, err := canonicalPath(cp.ContentDir)
	if err != nil {
		return DiscoveryResult{}, fmt.Errorf("resolve content root: %w", err)
	}

	var selected []string
	err = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 || entry.IsDir() {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		canonicalFile, err := canonicalPath(filePath)
		if err != nil {
			return err
		}
		if !withinRoot(root, canonicalFile) {
			return fmt.Errorf("selected file escapes content root: %s", filePath)
		}

		relativePath, err := filepath.Rel(root, canonicalFile)
		if err != nil {
			return err
		}
		relativePath = filepath.ToSlash(relativePath)
		if !matchesAny(patterns, relativePath) || matchesAny(excludes, relativePath) {
			return nil
		}
		selected = append(selected, canonicalFile)
		return nil
	})
	if err != nil {
		return DiscoveryResult{}, err
	}
	if len(selected) == 0 {
		return DiscoveryResult{}, fmt.Errorf("configured index matched no files")
	}

	sort.Strings(selected)
	return buildCatalog(ctx, root, selected, scheme, false)
}

// DiscoverResources returns legacy mcp-resources definitions for callers that
// only need source metadata.
func DiscoverResources(cp *content.ContentProvider, scheme string) ([]ResourceDefinition, error) {
	result, err := Discover(context.Background(), cp, nil, scheme)
	if err != nil {
		return nil, err
	}
	return result.Sources, nil
}

func discoverLegacy(ctx context.Context, cp *content.ContentProvider, scheme string) (DiscoveryResult, error) {
	root, err := canonicalPath(cp.ResourcesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return DiscoveryResult{}, nil
		}
		return DiscoveryResult{}, fmt.Errorf("resolve resources root: %w", err)
	}

	var files []string
	err = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 || !isMarkdown(filePath) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		files = append(files, filePath)
		return nil
	})
	if err != nil {
		return DiscoveryResult{}, err
	}
	sort.Strings(files)

	result := DiscoveryResult{}
	for _, filePath := range files {
		if err := ctx.Err(); err != nil {
			return DiscoveryResult{}, err
		}
		raw, err := os.ReadFile(filePath)
		if err != nil {
			slog.Warn("Skipping invalid resource file", "file", filepath.Base(filePath), "error", err)
			continue
		}
		parsed, err := content.ParseMarkdown(raw, content.FrontmatterRequired)
		if err != nil {
			slog.Warn("Skipping invalid resource file", "file", filepath.Base(filePath), "error", err)
			continue
		}
		name, _ := parsed.Metadata["name"].(string)
		description, _ := parsed.Metadata["description"].(string)
		if name == "" || description == "" {
			slog.Warn("Skipping resource with missing metadata", "file", filepath.Base(filePath))
			continue
		}
		relativePath, err := filepath.Rel(root, filePath)
		if err != nil {
			return DiscoveryResult{}, err
		}
		uri := sourceURI(scheme, relativePath)
		source, err := content.BuildSourceDocument(parsed, content.SourceOptions{
			URI: uri, FilePath: filePath, RelativePath: filepath.ToSlash(relativePath), Raw: raw,
		})
		if err != nil {
			slog.Warn("Skipping invalid resource file", "file", filepath.Base(filePath), "error", err)
			continue
		}
		chunks, err := content.ChunkMarkdown(source, parsed)
		if err != nil {
			slog.Warn("Skipping invalid resource file", "file", filepath.Base(filePath), "error", err)
			continue
		}
		result.Sources = append(result.Sources, source)
		result.Chunks = append(result.Chunks, chunks...)
		slog.Info("Loaded resource", "uri", uri, "name", source.Name)
	}
	return result, nil
}

func buildCatalog(ctx context.Context, root string, files []string, scheme string, requiredFrontmatter bool) (DiscoveryResult, error) {
	result := DiscoveryResult{}
	mode := content.FrontmatterOptional
	if requiredFrontmatter {
		mode = content.FrontmatterRequired
	}
	for _, filePath := range files {
		if err := ctx.Err(); err != nil {
			return DiscoveryResult{}, err
		}
		if !isMarkdown(filePath) {
			return DiscoveryResult{}, fmt.Errorf("configured index selected non-Markdown file: %s", filePath)
		}
		raw, err := os.ReadFile(filePath)
		if err != nil {
			return DiscoveryResult{}, fmt.Errorf("read configured file %s: %w", filePath, err)
		}
		parsed, err := content.ParseMarkdown(raw, mode)
		if err != nil {
			return DiscoveryResult{}, fmt.Errorf("parse configured file %s: %w", filePath, err)
		}
		relativePath, err := filepath.Rel(root, filePath)
		if err != nil {
			return DiscoveryResult{}, err
		}
		uri := sourceURI(scheme, relativePath)
		source, err := content.BuildSourceDocument(parsed, content.SourceOptions{
			URI: uri, FilePath: filePath, RelativePath: filepath.ToSlash(relativePath), Raw: raw,
		})
		if err != nil {
			return DiscoveryResult{}, fmt.Errorf("build configured source %s: %w", filePath, err)
		}
		chunks, err := content.ChunkMarkdown(source, parsed)
		if err != nil {
			return DiscoveryResult{}, fmt.Errorf("chunk configured source %s: %w", filePath, err)
		}
		result.Sources = append(result.Sources, source)
		result.Chunks = append(result.Chunks, chunks...)
	}
	return result, nil
}

func validatePatterns(patterns []string) ([]string, error) {
	validated := make([]string, len(patterns))
	for i, pattern := range patterns {
		normalized := strings.ReplaceAll(pattern, "\\", "/")
		if filepath.IsAbs(pattern) || path.IsAbs(normalized) || filepath.VolumeName(pattern) != "" || hasVolumePrefix(normalized) {
			return nil, fmt.Errorf("index pattern must be relative: %s", pattern)
		}
		cleaned := path.Clean(normalized)
		if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			return nil, fmt.Errorf("index pattern escapes content root: %s", pattern)
		}
		if !doublestar.ValidatePattern(normalized) {
			return nil, fmt.Errorf("invalid index pattern: %s", pattern)
		}
		validated[i] = normalized
	}
	return validated, nil
}

func hasVolumePrefix(pattern string) bool {
	return len(pattern) >= 2 && pattern[1] == ':' && ((pattern[0] >= 'a' && pattern[0] <= 'z') || (pattern[0] >= 'A' && pattern[0] <= 'Z'))
}

func canonicalPath(filePath string) (string, error) {
	resolved, err := filepath.EvalSymlinks(filePath)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

func withinRoot(root, candidate string) bool {
	relativePath, err := filepath.Rel(root, candidate)
	return err == nil && relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) && !filepath.IsAbs(relativePath)
}

func matchesAny(patterns []string, name string) bool {
	for _, pattern := range patterns {
		matched, err := doublestar.Match(pattern, name)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func isMarkdown(filePath string) bool {
	extension := strings.ToLower(filepath.Ext(filePath))
	return extension == ".md" || extension == ".markdown"
}

func sourceURI(scheme, relativePath string) string {
	withoutExtension := strings.TrimSuffix(relativePath, filepath.Ext(relativePath))
	return fmt.Sprintf("%s://%s", scheme, filepath.ToSlash(withoutExtension))
}
