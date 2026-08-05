package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestResolveMetadata_MissingManifestFallsBackToDefaults(t *testing.T) {
	contentDir := filepath.Join(t.TempDir(), "my-repo")
	require.NoError(t, os.MkdirAll(contentDir, 0o755))
	cp := content.NewContentProvider(contentDir)

	resolved, err := resolveMetadata(cp, "1.2.3")

	require.NoError(t, err)
	require.True(t, resolved.defaulted)
	require.Equal(t, domain.DefaultMetadata("my-repo", "1.2.3"), resolved.metadata)
}

func TestResolveMetadata_ValidManifestReturnedVerbatim(t *testing.T) {
	contentDir := t.TempDir()
	manifest := `
server:
  name: test
  version: "1.0"
  instructions: inst
tools:
  - name: search
    description: custom search
`
	cp := content.NewContentProvider(contentDir)
	require.NoError(t, os.MkdirAll(cp.ConfigDir, 0o755))
	require.NoError(t, os.WriteFile(cp.ConfigFile, []byte(manifest), 0o644))

	resolved, err := resolveMetadata(cp, "9.9.9")

	require.NoError(t, err)
	require.False(t, resolved.defaulted)
	require.Equal(t, "test", resolved.metadata.Server.Name)
	require.Equal(t, "1.0", resolved.metadata.Server.Version)
	require.Equal(t, "inst", resolved.metadata.Server.Instructions)
	require.Equal(t, []domain.ToolMetadata{{Name: "search", Description: "custom search"}}, resolved.metadata.Tools)
}

func TestResolveMetadata_MalformedYAMLReturnsError(t *testing.T) {
	contentDir := t.TempDir()
	cp := content.NewContentProvider(contentDir)
	require.NoError(t, os.MkdirAll(cp.ConfigDir, 0o755))
	require.NoError(t, os.WriteFile(cp.ConfigFile, []byte("not: valid: yaml: {{"), 0o644))

	_, err := resolveMetadata(cp, "1.0.0")

	require.ErrorContains(t, err, "failed to parse metadata")
}

func TestResolveMetadata_ValidationFailureReturnsError(t *testing.T) {
	contentDir := t.TempDir()
	manifest := `
server:
  name: ""
  version: ""
  instructions: ""
`
	cp := content.NewContentProvider(contentDir)
	require.NoError(t, os.MkdirAll(cp.ConfigDir, 0o755))
	require.NoError(t, os.WriteFile(cp.ConfigFile, []byte(manifest), 0o644))

	_, err := resolveMetadata(cp, "1.0.0")

	require.ErrorContains(t, err, "metadata validation failed")
}

func TestResolveMetadata_UnreadableManifestReturnsError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission bits")
	}
	contentDir := t.TempDir()
	cp := content.NewContentProvider(contentDir)
	manifestPath := cp.ConfigFile
	require.NoError(t, os.MkdirAll(cp.ConfigDir, 0o755))
	require.NoError(t, os.WriteFile(manifestPath, []byte("server: {name: test, version: '1', instructions: i}"), 0o644))
	require.NoError(t, os.Chmod(manifestPath, 0o000))
	t.Cleanup(func() { _ = os.Chmod(manifestPath, 0o644) })

	_, err := resolveMetadata(cp, "1.0.0")

	require.ErrorContains(t, err, "failed to read metadata file")
}

// TestResolveMetadata_ManifestIsDirectoryReturnsError exercises the
// unreadable-manifest path with a failure mode that reproduces on every uid
// and platform (EISDIR), unlike the permission-bit test above which is
// skipped under root.
func TestResolveMetadata_ManifestIsDirectoryReturnsError(t *testing.T) {
	contentDir := t.TempDir()
	require.NoError(t, os.MkdirAll(content.NewContentProvider(contentDir).ConfigFile, 0o755))
	cp := content.NewContentProvider(contentDir)

	_, err := resolveMetadata(cp, "1.0.0")

	require.ErrorContains(t, err, "failed to read metadata file")
}

// TestResolveMetadata_NonexistentContentDirReturnsError pins the diagnostic
// for a typo'd --content-dir: os.ReadFile("<typo>/.acdc/config.yaml") also
// satisfies fs.ErrNotExist, so without an explicit content-dir check the
// server would log a misleading "using built-in defaults" for a directory
// that was never resolved, then fail one step later on content root
// resolution.
func TestResolveMetadata_NonexistentContentDirReturnsError(t *testing.T) {
	contentDir := filepath.Join(t.TempDir(), "does-not-exist")
	cp := content.NewContentProvider(contentDir)

	_, err := resolveMetadata(cp, "1.0.0")

	require.ErrorContains(t, err, "content directory")
	require.ErrorContains(t, err, contentDir)
}

func TestResolveMetadata_ContentDirIsFileReturnsError(t *testing.T) {
	parent := t.TempDir()
	contentDir := filepath.Join(parent, "not-a-directory")
	require.NoError(t, os.WriteFile(contentDir, []byte("not a directory"), 0o644))
	cp := content.NewContentProvider(contentDir)

	_, err := resolveMetadata(cp, "1.0.0")

	require.ErrorContains(t, err, "content directory")
	require.ErrorContains(t, err, contentDir)
}

func TestRepoBaseName(t *testing.T) {
	wd, err := os.Getwd()
	require.NoError(t, err)

	tests := []struct {
		name       string
		contentDir string
		want       string
	}{
		{name: "nested path", contentDir: "/foo/bar/my-repo", want: "my-repo"},
		{name: "trailing slash", contentDir: "/foo/bar/my-repo/", want: "my-repo"},
		{name: "dot resolves to working directory name", contentDir: ".", want: filepath.Base(wd)},
		{name: "root has no meaningful base name", contentDir: "/", want: "Repository"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, repoBaseName(tt.contentDir))
		})
	}
}

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "empty falls back to placeholder", version: "", want: "0.0.0"},
		{name: "non-empty passes through", version: "1.2.3", want: "1.2.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, resolveVersion(tt.version))
		})
	}
}
