package resources

import (
	"context"
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
