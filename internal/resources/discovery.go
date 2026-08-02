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

// discoveryOps keeps filesystem operations explicit for deterministic error
// handling tests. Each Discover call receives its own immutable value.
type discoveryOps struct {
	canonicalPath func(string) (string, error)
	walkDir       func(string, fs.WalkDirFunc) error
	readFile      func(string) ([]byte, error)
	relativePath  func(string, string) (string, error)
}

func defaultDiscoveryOps() discoveryOps {
	return discoveryOps{
		canonicalPath: canonicalPath,
		walkDir:       filepath.WalkDir,
		readFile:      os.ReadFile,
		relativePath:  filepath.Rel,
	}
}

// discoverPolicy controls how the configured-index path tolerates repository
// content. It never affects validation of the invocation itself (patterns,
// content root, selected file containment or type), only how missing or
// unreadable/unparsable repository files are handled.
type discoverPolicy struct {
	lenient bool
}

// DiscoverOption configures discovery behavior.
type DiscoverOption func(*discoverPolicy)

// WithLenientIndex tolerates an empty selection and skips files that cannot be read
// or parsed, instead of failing startup. Used when the index configuration is
// defaulted rather than declared by an operator. It has no effect in legacy
// mcp-resources mode (index == nil), which resolves before any policy is applied.
func WithLenientIndex() DiscoverOption {
	return func(p *discoverPolicy) { p.lenient = true }
}

func resolvePolicy(opts []DiscoverOption) discoverPolicy {
	var policy discoverPolicy
	for _, opt := range opts {
		opt(&policy)
	}
	return policy
}

// directories that never hold indexable documentation.
var alwaysPrunedDirs = map[string]bool{
	".git": true,
}

// build/dependency artifact directories pruned only under the lenient
// policy, so an explicit manifest keeps selecting exactly what it selects
// today.
var lenientPrunedDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"target":       true,
	".venv":        true,
}

func shouldPruneDir(name string, lenient bool) bool {
	if alwaysPrunedDirs[name] {
		return true
	}
	return lenient && lenientPrunedDirs[name]
}

// Discover loads configured Markdown sources, or legacy mcp-resources when
// index is nil. By default it applies today's strict behavior: an empty
// selection or an unreadable/unparsable configured file fails discovery.
// Pass WithLenientIndex to tolerate repository content issues instead.
func Discover(ctx context.Context, cp *content.ContentProvider, index *domain.IndexMetadata, scheme string, opts ...DiscoverOption) (DiscoveryResult, error) {
	return discoverWithOps(ctx, cp, index, scheme, defaultDiscoveryOps(), opts...)
}

func discoverWithOps(ctx context.Context, cp *content.ContentProvider, index *domain.IndexMetadata, scheme string, ops discoveryOps, opts ...DiscoverOption) (DiscoveryResult, error) {
	if index == nil {
		return discoverLegacy(ctx, cp, scheme, ops)
	}
	policy := resolvePolicy(opts)

	patterns, err := validatePatterns(index.Include)
	if err != nil {
		return DiscoveryResult{}, err
	}
	excludes, err := validatePatterns(index.Exclude)
	if err != nil {
		return DiscoveryResult{}, err
	}

	root, err := ops.canonicalPath(cp.ContentDir)
	if err != nil {
		return DiscoveryResult{}, fmt.Errorf("resolve content root: %w", err)
	}

	var selected []string
	err = ops.walkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if filePath != root && shouldPruneDir(entry.Name(), policy.lenient) {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		canonicalFile, err := ops.canonicalPath(filePath)
		if err != nil {
			return err
		}
		if !withinRoot(root, canonicalFile) {
			return fmt.Errorf("selected file escapes content root: %s", filePath)
		}

		relativePath, err := ops.relativePath(root, canonicalFile)
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
		if policy.lenient {
			slog.Warn("Configured index matched no files", "root", root)
			return DiscoveryResult{}, nil
		}
		return DiscoveryResult{}, fmt.Errorf("configured index matched no files")
	}

	sort.Strings(selected)
	return buildCatalog(ctx, root, selected, scheme, ops, policy)
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

func discoverLegacy(ctx context.Context, cp *content.ContentProvider, scheme string, ops discoveryOps) (DiscoveryResult, error) {
	root, err := ops.canonicalPath(cp.ResourcesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return DiscoveryResult{}, nil
		}
		return DiscoveryResult{}, fmt.Errorf("resolve resources root: %w", err)
	}

	var files []string
	err = ops.walkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
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
		raw, err := ops.readFile(filePath)
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
		relativePath, err := ops.relativePath(root, filePath)
		if err != nil {
			return DiscoveryResult{}, err
		}
		uri := sourceURI(scheme, relativePath)
		source := buildParsedSource(parsed, content.SourceOptions{
			URI: uri, FilePath: filePath, RelativePath: filepath.ToSlash(relativePath), Raw: raw,
		})
		chunks := chunkParsedSource(source, parsed)
		result.Sources = append(result.Sources, source)
		result.Chunks = append(result.Chunks, chunks...)
		slog.Info("Loaded resource", "uri", uri, "name", source.Name)
	}
	return result, nil
}

func buildCatalog(ctx context.Context, root string, files []string, scheme string, ops discoveryOps, policy discoverPolicy) (DiscoveryResult, error) {
	result := DiscoveryResult{}
	for _, filePath := range files {
		if err := ctx.Err(); err != nil {
			return DiscoveryResult{}, err
		}
		if !isMarkdown(filePath) {
			return DiscoveryResult{}, fmt.Errorf("configured index selected non-Markdown file: %s", filePath)
		}
		raw, err := ops.readFile(filePath)
		if err != nil {
			if policy.lenient {
				slog.Warn("Skipping unreadable configured file", "file", filepath.Base(filePath), "error", err)
				continue
			}
			return DiscoveryResult{}, fmt.Errorf("read configured file %s: %w", filePath, err)
		}
		parsed, err := content.ParseMarkdown(raw, content.FrontmatterOptional)
		if err != nil {
			if policy.lenient {
				slog.Warn("Skipping invalid configured file", "file", filepath.Base(filePath), "error", err)
				continue
			}
			return DiscoveryResult{}, fmt.Errorf("parse configured file %s: %w", filePath, err)
		}
		relativePath, err := ops.relativePath(root, filePath)
		if err != nil {
			return DiscoveryResult{}, err
		}
		uri := sourceURI(scheme, relativePath)
		source := buildParsedSource(parsed, content.SourceOptions{
			URI: uri, FilePath: filePath, RelativePath: filepath.ToSlash(relativePath), Raw: raw,
		})
		chunks := chunkParsedSource(source, parsed)
		result.Sources = append(result.Sources, source)
		result.Chunks = append(result.Chunks, chunks...)
	}
	return result, nil
}

// ParseMarkdown returns a non-nil ParsedMarkdown with an AST on success.
// The two construction helpers reject only that invalid input state, so their
// errors are unreachable after a successful parse.
func buildParsedSource(parsed *content.ParsedMarkdown, opts content.SourceOptions) domain.SourceDocument {
	source, _ := content.BuildSourceDocument(parsed, opts)
	return source
}

func chunkParsedSource(source domain.SourceDocument, parsed *content.ParsedMarkdown) []domain.Chunk {
	chunks, _ := content.ChunkMarkdown(source, parsed)
	return chunks
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
