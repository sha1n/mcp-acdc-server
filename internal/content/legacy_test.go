package content

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetectLegacyLayout_CleanRoot(t *testing.T) {
	require.Empty(t, DetectLegacyLayout(t.TempDir()))
}

func TestDetectLegacyLayout_FindsEachMarker(t *testing.T) {
	tests := []struct {
		name    string
		create  func(t *testing.T, root string)
		wantLen int
		wantOld string
		wantNew string
	}{
		{
			name:    "manifest",
			create:  func(t *testing.T, root string) { writeLegacyFile(t, root, "mcp-metadata.yaml") },
			wantLen: 1,
			wantOld: "mcp-metadata.yaml",
			wantNew: ".acdc/config.yaml",
		},
		{
			name:    "prompts dir",
			create:  func(t *testing.T, root string) { writeLegacyDir(t, root, "mcp-prompts") },
			wantLen: 1,
			wantOld: "mcp-prompts/",
			wantNew: ".acdc/prompts/",
		},
		{
			// The resources marker only fires when a manifest is present
			// somewhere (see TestDetectLegacyLayout_ResourcesMarkerRequiresManifest
			// for that narrowing rule in isolation), so this case pairs it with
			// one to keep exercising marker detection itself.
			name: "resources dir",
			create: func(t *testing.T, root string) {
				writeLegacyFile(t, root, "mcp-metadata.yaml")
				writeLegacyDir(t, root, "mcp-resources")
			},
			wantLen: 2,
			wantOld: "mcp-resources/",
			wantNew: ".acdc/resources/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			tt.create(t, root)

			found := DetectLegacyLayout(root)

			require.Len(t, found, tt.wantLen)
			require.Contains(t, found, LegacyPath{Old: tt.wantOld, New: tt.wantNew})
		})
	}
}

// The resources marker is a plausible directory name in any repository, so it
// only counts as a legacy layout signal when a manifest is present at either
// location. Without one, zero-config discovery never scans mcp-resources/
// anyway (it routes through the configured-index path), so there is no silent
// behavior to convert into a visible refusal.
func TestDetectLegacyLayout_ResourcesMarkerRequiresManifest(t *testing.T) {
	t.Run("bare, no manifest anywhere", func(t *testing.T) {
		root := t.TempDir()
		writeLegacyDir(t, root, "mcp-resources")

		require.Empty(t, DetectLegacyLayout(root))
	})

	t.Run("alongside current .acdc/config.yaml", func(t *testing.T) {
		root := t.TempDir()
		writeLegacyDir(t, root, "mcp-resources")
		require.NoError(t, os.MkdirAll(filepath.Join(root, ConfigDirName), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, ConfigDirName, ConfigFileName), []byte("x"), 0o600))

		found := DetectLegacyLayout(root)

		require.Contains(t, found, LegacyPath{Old: "mcp-resources/", New: ".acdc/resources/"})
	})

	t.Run("alongside legacy mcp-metadata.yaml", func(t *testing.T) {
		root := t.TempDir()
		writeLegacyDir(t, root, "mcp-resources")
		writeLegacyFile(t, root, "mcp-metadata.yaml")

		found := DetectLegacyLayout(root)

		require.Len(t, found, 2)
		require.Contains(t, found, LegacyPath{Old: "mcp-metadata.yaml", New: ".acdc/config.yaml"})
		require.Contains(t, found, LegacyPath{Old: "mcp-resources/", New: ".acdc/resources/"})
	})
}

// A stale name is reported regardless of what the operator left behind, so a
// manifest name occupied by a directory cannot slip through as "no manifest".
func TestDetectLegacyLayout_IgnoresEntryType(t *testing.T) {
	root := t.TempDir()
	writeLegacyDir(t, root, "mcp-metadata.yaml")

	found := DetectLegacyLayout(root)

	require.Len(t, found, 1)
	require.Equal(t, "mcp-metadata.yaml", found[0].Old)
}

func TestDetectLegacyLayout_ReportsAllInStableOrder(t *testing.T) {
	root := t.TempDir()
	writeLegacyDir(t, root, "mcp-resources")
	writeLegacyDir(t, root, "mcp-prompts")
	writeLegacyFile(t, root, "mcp-metadata.yaml")

	found := DetectLegacyLayout(root)

	require.Len(t, found, 3)
	require.Equal(t, "mcp-metadata.yaml", found[0].Old)
	require.Equal(t, "mcp-prompts/", found[1].Old)
	require.Equal(t, "mcp-resources/", found[2].Old)
}

// Detection is unconditional: a half-migrated root is exactly the case that
// would otherwise lose prompts with no error.
func TestDetectLegacyLayout_FiresWhenNewLayoutAlsoPresent(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ConfigDirName), 0o755))
	writeLegacyDir(t, root, "mcp-prompts")

	require.Len(t, DetectLegacyLayout(root), 1)
}

func TestDetectLegacyLayout_MissingRoot(t *testing.T) {
	require.Empty(t, DetectLegacyLayout(filepath.Join(t.TempDir(), "nope")))
}

func writeLegacyFile(t *testing.T, root, name string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600))
}

func writeLegacyDir(t *testing.T, root, name string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, name), 0o755))
}
