package resources

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestDiscover_ConfiguredSources(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/guide.md", "# Guide\n\nUseful guide.\n")
	writeFile(t, root, "docs/api/auth.md", "# Auth\n\nToken details.\n")
	writeFile(t, root, "docs/generated/skip.md", "# Skip\n")

	result, err := Discover(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{
		Include: []string{"docs/**/*.md"},
		Exclude: []string{"docs/generated/**"},
	}, "acdc")
	require.NoError(t, err)
	require.Equal(t, []string{"acdc://docs/api/auth", "acdc://docs/guide"}, sourceURIs(result.Sources))
	require.NotEmpty(t, result.Chunks)
}

func TestDiscover_ConfiguredValidation(t *testing.T) {
	tests := []struct {
		name    string
		include []string
		wantErr string
	}{
		{name: "absolute", include: []string{"/tmp/*.md"}, wantErr: "index pattern must be relative"},
		{name: "escape", include: []string{"../docs/*.md"}, wantErr: "index pattern escapes content root"},
		{name: "bad pattern", include: []string{"docs/[.md"}, wantErr: "invalid index pattern"},
		{name: "zero matches", include: []string{"docs/**/*.md"}, wantErr: "configured index matched no files"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Discover(context.Background(), content.NewContentProvider(t.TempDir()), &domain.IndexMetadata{Include: tt.include}, "acdc")
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestDiscover_ConfiguredRejectsInvalidExcludeAndMissingRoot(t *testing.T) {
	root := t.TempDir()

	_, err := Discover(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{
		Include: []string{"docs/**/*.md"},
		Exclude: []string{"/private/*.md"},
	}, "acdc")
	require.ErrorContains(t, err, "index pattern must be relative")

	_, err = Discover(context.Background(), content.NewContentProvider(filepath.Join(root, "missing")), &domain.IndexMetadata{
		Include: []string{"docs/**/*.md"},
	}, "acdc")
	require.ErrorContains(t, err, "resolve content root")
}

func TestDiscover_ConfiguredMatching(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/top.md", "# Top\n")
	writeFile(t, root, "docs/nested/guide.markdown", "# Nested\n")
	writeFile(t, root, "docs/ignored.md", "# Ignored\n")

	result, err := Discover(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{
		Include: []string{"docs/**/*.md", "docs/**/*.*"},
		Exclude: []string{"docs/ignored.md"},
	}, "acdc")
	require.NoError(t, err)
	require.Equal(t, []string{"acdc://docs/nested/guide", "acdc://docs/top"}, sourceURIs(result.Sources))
}

func TestDiscover_ConfiguredRejectsSelectedNonMarkdown(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/readme.txt", "not markdown")

	_, err := Discover(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{Include: []string{"docs/**"}}, "acdc")
	require.ErrorContains(t, err, "configured index selected non-Markdown file")
}

func TestDiscover_ConfiguredFailsForSelectedFileErrors(t *testing.T) {
	t.Run("invalid explicit frontmatter", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "docs/bad.md", "---\nname: [\n---\n# Bad\n")

		_, err := Discover(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{Include: []string{"docs/*.md"}}, "acdc")
		require.ErrorContains(t, err, "invalid YAML in frontmatter")
	})

}

func TestDiscover_ConfiguredCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Discover(ctx, content.NewContentProvider(t.TempDir()), &domain.IndexMetadata{Include: []string{"**/*.md"}}, "acdc")
	require.ErrorIs(t, err, context.Canceled)
}

func TestDiscover_ConfiguredSkipsSymlinksAndPreservesContainment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires additional permissions on Windows")
	}
	root := t.TempDir()
	external := t.TempDir()
	writeFile(t, root, "docs/inside.md", "# Inside\n")
	outside := writeFile(t, external, "outside.md", "# Outside\n")
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "docs", "outside.md")))
	require.NoError(t, os.Symlink(external, filepath.Join(root, "docs", "linked-dir")))

	result, err := Discover(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{Include: []string{"docs/**/*.md"}}, "acdc")
	require.NoError(t, err)
	require.Equal(t, []string{"acdc://docs/inside"}, sourceURIs(result.Sources))
}

func TestDiscover_LegacySkipsInvalidFilesAndBuildsChunks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "mcp-resources/valid.md", "---\nname: Valid\ndescription: Valid description\n---\n# Valid\n\nBody.\n")
	writeFile(t, root, "mcp-resources/broken.md", "---\nname: [\n---\n# Broken\n")
	writeFile(t, root, "mcp-resources/missing.md", "---\nname: Missing description\n---\n# Missing\n")
	writeFile(t, root, "mcp-resources/ignore.txt", "not a resource")

	result, err := Discover(context.Background(), content.NewContentProvider(root), nil, "acdc")
	require.NoError(t, err)
	require.Equal(t, []string{"acdc://valid"}, sourceURIs(result.Sources))
	require.NotEmpty(t, result.Chunks)
	require.Equal(t, "# Valid\n\nBody.\n", result.Sources[0].Content)
}

func TestDiscover_LegacyMissingDirectoryAndCancellation(t *testing.T) {
	root := t.TempDir()

	definitions, err := DiscoverResources(content.NewContentProvider(root), "acdc")
	require.NoError(t, err)
	require.Empty(t, definitions)

	resourcesDir := filepath.Join(root, "mcp-resources")
	require.NoError(t, os.MkdirAll(resourcesDir, 0o755))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Discover(ctx, content.NewContentProvider(root), nil, "acdc")
	require.ErrorIs(t, err, context.Canceled)
}

func TestDiscover_LegacyIgnoresLenientOption(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "mcp-resources/valid.md", "---\nname: Valid\ndescription: Valid description\n---\n# Valid\n\nBody.\n")
	writeFile(t, root, "mcp-resources/no-frontmatter.md", "# No Frontmatter\n\nBody.\n")

	strict, err := Discover(context.Background(), content.NewContentProvider(root), nil, "acdc")
	require.NoError(t, err)
	require.Equal(t, []string{"acdc://valid"}, sourceURIs(strict.Sources))

	lenient, err := Discover(context.Background(), content.NewContentProvider(root), nil, "acdc", WithLenientIndex())
	require.NoError(t, err)

	require.Equal(t, strict, lenient)
}

func TestDiscoverResources_FailsForUnresolvableLegacyRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires additional permissions on Windows")
	}
	root := t.TempDir()
	resourcesDir := filepath.Join(root, "mcp-resources")
	require.NoError(t, os.Symlink(resourcesDir, resourcesDir))

	_, err := DiscoverResources(content.NewContentProvider(root), "acdc")
	require.ErrorContains(t, err, "resolve resources root")
}

func TestDiscover_LenientZeroMatchesReturnsEmptyResult(t *testing.T) {
	root := t.TempDir()

	result, err := Discover(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{
		Include: []string{"docs/**/*.md"},
	}, "acdc", WithLenientIndex())
	require.NoError(t, err)
	require.Empty(t, result.Sources)
	require.Empty(t, result.Chunks)
}

func TestDiscover_LenientSkipsUnparsableFilesButIndexesSiblings(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/good.md", "# Good\n\nBody.\n")
	writeFile(t, root, "docs/bad.md", "---\nname: [\n---\n# Bad\n")

	result, err := Discover(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{
		Include: []string{"docs/*.md"},
	}, "acdc", WithLenientIndex())
	require.NoError(t, err)
	require.Equal(t, []string{"acdc://docs/good"}, sourceURIs(result.Sources))

	_, err = Discover(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{
		Include: []string{"docs/*.md"},
	}, "acdc")
	require.ErrorContains(t, err, "invalid YAML in frontmatter")
}

func TestDiscover_LenientStillRejectsRootEscapingPatterns(t *testing.T) {
	root := t.TempDir()

	_, err := Discover(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{
		Include: []string{"../docs/*.md"},
	}, "acdc", WithLenientIndex())
	require.ErrorContains(t, err, "index pattern escapes content root")
}

func TestDiscover_PrunesGitDirectoryUnderBothPolicies(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".git/x.md", "# Git\n")
	writeFile(t, root, "docs/guide.md", "# Guide\n")

	for _, opts := range [][]DiscoverOption{nil, {WithLenientIndex()}} {
		result, err := Discover(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{
			Include: []string{"**/*.md"},
		}, "acdc", opts...)
		require.NoError(t, err)
		require.Equal(t, []string{"acdc://docs/guide"}, sourceURIs(result.Sources))
	}
}

func TestDiscover_PrunesBuildArtifactDirectoriesOnlyWhenLenient(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "node_modules/x.md", "# Vendored\n")
	writeFile(t, root, "docs/guide.md", "# Guide\n")

	strict, err := Discover(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{
		Include: []string{"**/*.md"},
	}, "acdc")
	require.NoError(t, err)
	require.Equal(t, []string{"acdc://docs/guide", "acdc://node_modules/x"}, sourceURIs(strict.Sources))

	lenient, err := Discover(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{
		Include: []string{"**/*.md"},
	}, "acdc", WithLenientIndex())
	require.NoError(t, err)
	require.Equal(t, []string{"acdc://docs/guide"}, sourceURIs(lenient.Sources))
}

func TestDiscover_PrunesNestedBuildArtifactDirectoriesOnlyWhenLenient(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/build/generated.md", "# Generated\n")
	writeFile(t, root, "docs/guide.md", "# Guide\n")

	strict, err := Discover(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{
		Include: []string{"**/*.md"},
	}, "acdc")
	require.NoError(t, err)
	require.Equal(t, []string{"acdc://docs/build/generated", "acdc://docs/guide"}, sourceURIs(strict.Sources))

	lenient, err := Discover(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{
		Include: []string{"**/*.md"},
	}, "acdc", WithLenientIndex())
	require.NoError(t, err)
	require.Equal(t, []string{"acdc://docs/guide"}, sourceURIs(lenient.Sources))
}

func TestDiscover_DoesNotPruneContentRootItself(t *testing.T) {
	root := t.TempDir()
	buildRoot := filepath.Join(root, "build")
	require.NoError(t, os.MkdirAll(filepath.Join(buildRoot, "docs"), 0o755))
	writeFile(t, buildRoot, "docs/guide.md", "# Guide\n")

	result, err := Discover(context.Background(), content.NewContentProvider(buildRoot), &domain.IndexMetadata{
		Include: []string{"docs/**/*.md"},
	}, "acdc", WithLenientIndex())
	require.NoError(t, err)
	require.Equal(t, []string{"acdc://docs/guide"}, sourceURIs(result.Sources))
}

func TestDiscover_DescendsOnlyDirectoriesIncludePatternsCanReach(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "# Readme\n")
	writeFile(t, root, "docs/guide.md", "# Guide\n")
	writeFile(t, root, "docs/api/auth.md", "# Auth\n")
	writeFile(t, root, "src/main.md", "# Main\n")
	writeFile(t, root, "node_modules/pkg/readme.md", "# Vendored\n")

	everything := []string{".", "docs", "docs/api", "node_modules", "node_modules/pkg", "src"}
	tests := []struct {
		name          string
		include       []string
		wantDescended []string
		wantSources   []string
	}{
		{
			name:          "a literal base bounds traversal to its own subtree",
			include:       []string{"docs/**/*.md"},
			wantDescended: []string{".", "docs", "docs/api"},
			wantSources:   []string{"acdc://docs/api/auth", "acdc://docs/guide"},
		},
		{
			name:          "a root anchored pattern descends nothing",
			include:       []string{"README.md"},
			wantDescended: []string{"."},
			wantSources:   []string{"acdc://README"},
		},
		{
			name:          "a globstar descends everything",
			include:       []string{"**/*.md"},
			wantDescended: everything,
			wantSources: []string{
				"acdc://README", "acdc://docs/api/auth", "acdc://docs/guide",
				"acdc://node_modules/pkg/readme", "acdc://src/main",
			},
		},
		{
			name:          "a wildcard first segment descends everything it cannot rule out",
			include:       []string{"d*cs/**"},
			wantDescended: everything,
			wantSources:   []string{"acdc://docs/api/auth", "acdc://docs/guide"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var descended []string
			result, err := discoverWithOps(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{
				Include: tt.include,
			}, "acdc", recordingDiscoveryOps(&descended))
			require.NoError(t, err)
			require.Equal(t, tt.wantDescended, descended)
			require.Equal(t, tt.wantSources, sourceURIs(result.Sources))
		})
	}
}

func TestDiscover_DescendsTheAncestorsOfANestedPatternBase(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/b/guide.md", "# Guide\n")
	writeFile(t, root, "a/c/other.md", "# Other\n")

	var descended []string
	result, err := discoverWithOps(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{
		Include: []string{"a/b/*.md"},
	}, "acdc", recordingDiscoveryOps(&descended))
	require.NoError(t, err)
	require.Equal(t, []string{".", "a", "a/b"}, descended)
	require.Equal(t, []string{"acdc://a/b/guide"}, sourceURIs(result.Sources))
}

func TestDiscover_PrunesArtifactDirectoriesInsidePatternAdmittedSubtrees(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/guide.md", "# Guide\n")
	writeFile(t, root, "docs/build/generated.md", "# Generated\n")

	var descended []string
	result, err := discoverWithOps(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{
		Include: []string{"docs/**/*.md"},
	}, "acdc", recordingDiscoveryOps(&descended), WithLenientIndex())
	require.NoError(t, err)
	require.Equal(t, []string{".", "docs"}, descended)
	require.Equal(t, []string{"acdc://docs/guide"}, sourceURIs(result.Sources))
}

// Bounding traversal must never change what is selected.
func TestDiscover_BoundedTraversalPreservesSelection(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "# Readme\n")
	writeFile(t, root, "docs/guide.md", "# Guide\n")
	writeFile(t, root, "docs/api/auth.md", "# Auth\n")
	writeFile(t, root, "docs/generated/skip.md", "# Skip\n")
	writeFile(t, root, "adr/0001-record.md", "# Record\n")
	writeFile(t, root, "src/internal/notes.md", "# Notes\n")
	writeFile(t, root, "node_modules/pkg/readme.md", "# Vendored\n")

	tests := []struct {
		name    string
		include []string
		exclude []string
		opts    []DiscoverOption
		want    []string
	}{
		{
			name:    "zero-config defaults",
			include: []string{"README.md", "docs/**/*.md"},
			exclude: []string{"docs/generated/**"},
			want:    []string{"acdc://README", "acdc://docs/api/auth", "acdc://docs/guide"},
		},
		{
			name:    "everything under a strict policy",
			include: []string{"**/*.md"},
			want: []string{
				"acdc://README", "acdc://adr/0001-record", "acdc://docs/api/auth",
				"acdc://docs/generated/skip", "acdc://docs/guide",
				"acdc://node_modules/pkg/readme", "acdc://src/internal/notes",
			},
		},
		{
			name:    "everything under a lenient policy",
			include: []string{"**/*.md"},
			opts:    []DiscoverOption{WithLenientIndex()},
			want: []string{
				"acdc://README", "acdc://adr/0001-record", "acdc://docs/api/auth",
				"acdc://docs/generated/skip", "acdc://docs/guide", "acdc://src/internal/notes",
			},
		},
		{
			name:    "a bare globstar",
			include: []string{"**"},
			want: []string{
				"acdc://README", "acdc://adr/0001-record", "acdc://docs/api/auth",
				"acdc://docs/generated/skip", "acdc://docs/guide",
				"acdc://node_modules/pkg/readme", "acdc://src/internal/notes",
			},
		},
		{
			name:    "braced alternatives",
			include: []string{"{docs,adr}/**/*.md"},
			want: []string{
				"acdc://adr/0001-record", "acdc://docs/api/auth",
				"acdc://docs/generated/skip", "acdc://docs/guide",
			},
		},
		{
			name:    "several disjoint bases",
			include: []string{"src/**/*.md", "adr/*.md"},
			want:    []string{"acdc://adr/0001-record", "acdc://src/internal/notes"},
		},
		{
			name:    "a nested base reached through its ancestors",
			include: []string{"docs/api/*.md"},
			want:    []string{"acdc://docs/api/auth"},
		},
		{
			name:    "root level files only",
			include: []string{"*.md"},
			want:    []string{"acdc://README"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Discover(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{
				Include: tt.include,
				Exclude: tt.exclude,
			}, "acdc", tt.opts...)
			require.NoError(t, err)
			require.Equal(t, tt.want, sourceURIs(result.Sources))
		})
	}
}

// recordingDiscoveryOps walks the real filesystem while recording the directories
// the walk actually descended into, as slash paths relative to the content root.
func recordingDiscoveryOps(descended *[]string) discoveryOps {
	ops := defaultDiscoveryOps()
	ops.walkDir = func(root string, walk fs.WalkDirFunc) error {
		return filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
			err := walk(filePath, entry, walkErr)
			if walkErr != nil || err != nil || !entry.IsDir() {
				return err
			}
			relativePath, relativeErr := filepath.Rel(root, filePath)
			if relativeErr != nil {
				return relativeErr
			}
			*descended = append(*descended, filepath.ToSlash(relativePath))
			return nil
		})
	}
	return ops
}

func writeFile(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func sourceURIs(sources []domain.SourceDocument) []string {
	uris := make([]string, len(sources))
	for i, source := range sources {
		uris[i] = source.URI
	}
	return uris
}
