package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCreateMCPServer_RefusesLegacyLayout(t *testing.T) {
	tests := []struct {
		name    string
		create  func(t *testing.T, root string)
		wantOld string
		wantNew string
	}{
		{"manifest", func(t *testing.T, root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "mcp-metadata.yaml"), []byte("x"), 0o600))
		}, "mcp-metadata.yaml", ".acdc/config.yaml"},
		{"prompts", func(t *testing.T, root string) {
			require.NoError(t, os.MkdirAll(filepath.Join(root, "mcp-prompts"), 0o755))
		}, "mcp-prompts/", ".acdc/prompts/"},
		{"resources with a legacy manifest present", func(t *testing.T, root string) {
			require.NoError(t, os.MkdirAll(filepath.Join(root, "mcp-resources"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "mcp-metadata.yaml"), []byte("x"), 0o600))
		}, "mcp-resources/", ".acdc/resources/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			tt.create(t, root)

			_, _, err := CreateMCPServer(context.Background(), &config.Settings{ContentDir: root}, "1.0.0")

			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantOld)
			require.Contains(t, err.Error(), tt.wantNew)
		})
	}
}

func TestCreateMCPServer_ReportsEveryLegacyMarker(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "mcp-metadata.yaml"), []byte("x"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "mcp-prompts"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "mcp-resources"), 0o755))

	_, _, err := CreateMCPServer(context.Background(), &config.Settings{ContentDir: root}, "1.0.0")

	require.Error(t, err)
	require.Contains(t, err.Error(), "mcp-metadata.yaml")
	require.Contains(t, err.Error(), "mcp-prompts/")
	require.Contains(t, err.Error(), "mcp-resources/")
}

// A half-migrated root must still refuse: the config moved but the prompts did
// not, and starting here would drop them silently.
func TestCreateMCPServer_RefusesHalfMigratedRoot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".acdc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".acdc", "config.yaml"),
		[]byte("server:\n  name: test\n  version: \"1.0\"\n  instructions: test\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "mcp-prompts"), 0o755))

	_, _, err := CreateMCPServer(context.Background(), &config.Settings{ContentDir: root}, "1.0.0")

	require.Error(t, err)
	require.Contains(t, err.Error(), "mcp-prompts/")
}

// A half-migrated root with a leftover mcp-resources/ must still refuse once
// the config has moved to .acdc/config.yaml: this is the case the resources
// marker's manifest-present narrowing must not lose sight of.
func TestCreateMCPServer_RefusesResourcesAlongsideCurrentConfig(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".acdc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".acdc", "config.yaml"),
		[]byte("server:\n  name: test\n  version: \"1.0\"\n  instructions: test\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "mcp-resources"), 0o755))

	_, _, err := CreateMCPServer(context.Background(), &config.Settings{ContentDir: root}, "1.0.0")

	require.Error(t, err)
	require.Contains(t, err.Error(), "mcp-resources/")
}

// A bare mcp-resources/ with no manifest anywhere must not refuse: zero-config
// discovery never scans it regardless (a non-nil Index always routes to the
// configured-index path), so there is no silent behavior change to convert
// into a refusal, and mcp-resources is too plausible a directory name in an
// unrelated repository to hard-fail startup over.
func TestCreateMCPServer_StartsOnBareResourcesDir(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "mcp-resources"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "README.md"), []byte("# Test\n\nBody.\n"), 0o600))

	_, cleanup, err := CreateMCPServer(context.Background(), appTestSettings(root), "1.0.0")

	require.NoError(t, err)
	cleanup()
}

// A mistyped --content-dir must keep reporting the directory problem rather
// than a misleading "no legacy layout here" pass-through.
func TestCreateMCPServer_MissingContentDirStillReportsDirectoryError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")

	_, _, err := CreateMCPServer(context.Background(), &config.Settings{ContentDir: missing}, "1.0.0")

	require.Error(t, err)
	require.Contains(t, err.Error(), "not found or not a directory")
}

// The refusal must not fire on a repository that has already migrated.
func TestCreateMCPServer_StartsOnMigratedRoot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".acdc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".acdc", "config.yaml"),
		[]byte("server:\n  name: test\n  version: \"1.0\"\n  instructions: test\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "README.md"), []byte("# Test\n\nBody.\n"), 0o600))

	_, cleanup, err := CreateMCPServer(context.Background(), appTestSettings(root), "1.0.0")

	require.NoError(t, err)
	cleanup()
}
