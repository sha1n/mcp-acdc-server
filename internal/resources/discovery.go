package resources

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

	root, selected, err := selectConfiguredFiles(ctx, cp, index, ops, policy)
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

	paths := make([]string, len(selected))
	for i, file := range selected {
		paths[i] = file.path
	}
	return buildCatalog(ctx, root, paths, scheme, ops, policy)
}

// selectedFile is a member of the file set a Discover call would select,
// with the cheap-to-obtain filesystem attributes Fingerprint needs. It never
// carries file content: that stays exclusive to buildCatalog and
// discoverLegacy, the callers that actually read a file's bytes.
type selectedFile struct {
	path         string // canonical absolute path
	relativePath string // slash-separated, relative to root
	size         int64
	modTime      time.Time
}

// selectConfiguredFiles walks cp.ContentDir and returns, in native canonical
// path order, every regular file matching index's include/exclude patterns under
// policy. It performs every check that must fail the invocation outright --
// pattern validity, root resolution, and root-escape containment -- but
// leaves the "selected nothing" decision to the caller, since that decision
// differs between Discover (fatal unless lenient) and Fingerprint (an empty
// selection is a legitimate digest).
func selectConfiguredFiles(ctx context.Context, cp *content.ContentProvider, index *domain.IndexMetadata, ops discoveryOps, policy discoverPolicy) (string, []selectedFile, error) {
	patterns, err := validatePatterns(index.Include)
	if err != nil {
		return "", nil, err
	}
	excludes, err := validatePatterns(index.Exclude)
	if err != nil {
		return "", nil, err
	}

	root, err := ops.canonicalPath(cp.ContentDir)
	if err != nil {
		return "", nil, fmt.Errorf("resolve content root: %w", err)
	}

	descent := newDescentFilter(patterns)

	var selected []selectedFile
	err = ops.walkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if filePath == root {
				return nil
			}
			if shouldPruneDir(entry.Name(), policy.lenient) {
				return fs.SkipDir
			}
			relativeDir, err := ops.relativePath(root, filePath)
			if err != nil {
				return err
			}
			if !descent.allows(filepath.ToSlash(relativeDir)) {
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
		selected = append(selected, selectedFile{
			path:         canonicalFile,
			relativePath: relativePath,
			size:         info.Size(),
			modTime:      info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return "", nil, err
	}

	sortSelectedFiles(selected)
	return root, selected, nil
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
	_, files, err := selectLegacyFiles(ctx, cp, ops)
	if err != nil {
		return DiscoveryResult{}, err
	}

	result := DiscoveryResult{}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return DiscoveryResult{}, err
		}
		raw, err := ops.readFile(file.path)
		if err != nil {
			slog.Warn("Skipping invalid resource file", "file", filepath.Base(file.path), "error", err)
			continue
		}
		parsed, err := content.ParseMarkdown(raw, content.FrontmatterRequired)
		if err != nil {
			slog.Warn("Skipping invalid resource file", "file", filepath.Base(file.path), "error", err)
			continue
		}
		name, _ := parsed.Metadata["name"].(string)
		description, _ := parsed.Metadata["description"].(string)
		if name == "" || description == "" {
			slog.Warn("Skipping resource with missing metadata", "file", filepath.Base(file.path))
			continue
		}
		uri := sourceURI(scheme, file.relativePath)
		if _, err := url.Parse(uri); err != nil {
			slog.Warn("Skipping invalid resource file", "file", filepath.Base(file.path), "error", err)
			continue
		}
		source := buildParsedSource(parsed, content.SourceOptions{
			URI: uri, FilePath: file.path, RelativePath: file.relativePath, Raw: raw,
		})
		chunks := chunkParsedSource(source, parsed)
		result.Sources = append(result.Sources, source)
		result.Chunks = append(result.Chunks, chunks...)
		slog.Info("Loaded resource", "uri", uri, "name", source.Name)
	}
	return result, nil
}

// selectLegacyFiles walks cp.ResourcesDir and returns, in native canonical
// path order, every regular Markdown file beneath it. A missing resources
// directory is not a configuration error in legacy mode -- it simply means
// there are no legacy resources -- so it yields an empty selection rather
// than propagating the stat failure.
func selectLegacyFiles(ctx context.Context, cp *content.ContentProvider, ops discoveryOps) (string, []selectedFile, error) {
	root, err := ops.canonicalPath(cp.ResourcesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, nil
		}
		return "", nil, fmt.Errorf("resolve resources root: %w", err)
	}

	var files []selectedFile
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
		relativePath, err := ops.relativePath(root, filePath)
		if err != nil {
			return err
		}
		files = append(files, selectedFile{
			path:         filePath,
			relativePath: filepath.ToSlash(relativePath),
			size:         info.Size(),
			modTime:      info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return "", nil, err
	}

	sortSelectedFiles(files)
	return root, files, nil
}

// sortSelectedFiles orders files by native canonical path, matching the order
// each selection walk produced before it moved behind this shared helper.
// Sorting by path rather than the slash-converted relativePath matters on a
// platform whose path separator is not '/': a file directly at a directory
// boundary can compare on opposite sides of a sibling name under the two
// forms (for example "aZ.md" vs. "a\sub.md": '\' is 0x5C, so it sorts after
// 'Z' natively but "/" is 0x2F, so the slash form would sort before it).
func sortSelectedFiles(files []selectedFile) {
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
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
		if _, err := url.Parse(uri); err != nil {
			if policy.lenient {
				slog.Warn("Skipping unaddressable configured file", "file", filepath.Base(filePath), "error", err)
				continue
			}
			return DiscoveryResult{}, fmt.Errorf("address configured file %s: %w", filePath, err)
		}
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

// canonicalPath absolutizes before resolving symlinks, so that a relative input
// resolves the working-directory prefix too. The reverse order leaves a relative
// content root unresolved while every walked file below it -- reached as an
// absolute path -- resolves, and containment then rejects the whole selection.
func canonicalPath(filePath string) (string, error) {
	absolute, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
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
