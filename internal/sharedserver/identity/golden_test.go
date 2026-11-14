//go:build !windows

package identity

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/stretchr/testify/require"
)

// The golden values below are bound to schemaVersion. A change to
// canonicalisation, the exclusion list or the digest coverage must fail one of
// these tests; fix it by bumping schemaVersion together with the golden
// values, never by updating the values alone.
const (
	goldenSchemaVersion     = 1
	goldenStampedIdentity   = "af61110b19cfb76b79b3d58040b8d4dd"
	goldenUnstampedIdentity = "5574235b8bcbc33e61ca5c8f2fa74e09"
	goldenContentDigest     = "b3fc12aa04a4bc707b5096b994b53cb7b9a1081ebf511bc5cbc19605b6e5daaa"
)

// goldenSettings uses "/" as the content dir because it is the one absolute,
// symlink-free directory whose path is identical on every Unix machine.
func goldenSettings() *config.Settings {
	return &config.Settings{
		ContentDir: "/",
		Transport:  "stdio",
		Host:       "0.0.0.0",
		Port:       8080,
		Scheme:     "acdc",
		CrossRef:   true,
		Search: config.SearchSettings{
			MaxResults:    10,
			InMemory:      true,
			KeywordsBoost: 3,
			HeadingBoost:  2.5,
			TitleBoost:    2,
			PathBoost:     1.25,
			ContentBoost:  1,
			ResultMode:    config.SearchResultModeReferences,
		},
		Auth:         config.AuthSettings{Type: config.AuthTypeNone},
		UI:           config.UISettings{MaxResults: 20},
		ServerMode:   config.ServerModeShared,
		SharedServer: config.SharedServerSettings{ConnectWait: 5 * time.Second, Linger: 30 * time.Second},
	}
}

func requireNoStartupContentAtRoot(t *testing.T) {
	t.Helper()
	if _, err := os.Lstat("/.acdc"); err == nil {
		t.Skip("/.acdc exists on this machine; the golden content dir must have no startup-fixed content")
	}
}

func TestGolden_SchemaVersion(t *testing.T) {
	require.Equal(t, goldenSchemaVersion, schemaVersion)
}

func TestGolden_ExclusionList(t *testing.T) {
	require.ElementsMatch(t, excludedSettingPaths, excludedSettings)
}

func TestGolden_StampedIdentity(t *testing.T) {
	requireNoStartupContentAtRoot(t)
	id := mustCompute(t, goldenSettings(), Binary{Version: "1.2.3", Build: "abc123"})
	require.Equal(t, goldenStampedIdentity, id)
}

func TestGolden_UnstampedIdentity(t *testing.T) {
	requireNoStartupContentAtRoot(t)
	exe := filepath.Join(t.TempDir(), "acdc-mcp")
	writeFile(t, exe, "golden executable bytes")
	id := mustCompute(t, goldenSettings(), Binary{Version: "dev", Build: "unknown", Executable: exe})
	require.Equal(t, goldenUnstampedIdentity, id)
}

func TestGolden_ContentDigestCoverage(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".acdc", "config.yaml"), "server:\n  name: golden\n")
	writeFile(t, filepath.Join(root, ".acdc", "prompts", "b.md"), "---\nname: b\n---\nB")
	writeFile(t, filepath.Join(root, ".acdc", "prompts", "a.md"), "---\nname: a\n---\nA")
	writeFile(t, filepath.Join(root, ".acdc", "prompts", "nested", "c.md"), "C")
	writeFile(t, filepath.Join(root, ".acdc", "prompts", "notes.txt"), "ignored")
	writeFile(t, filepath.Join(root, ".acdc", "prompts", "upper.MD"), "ignored")
	writeFile(t, filepath.Join(root, ".acdc", "resources", "r.md"), "ignored")
	writeFile(t, filepath.Join(root, ".acdc", "other.yaml"), "ignored")
	writeFile(t, filepath.Join(root, "doc.md"), "ignored")

	content, err := readStartupFixedContent(root)
	require.NoError(t, err)

	require.Equal(t, []string{
		".acdc/config.yaml",
		".acdc/prompts/a.md",
		".acdc/prompts/b.md",
		".acdc/prompts/nested/c.md",
	}, content.coveredPaths())
	require.Equal(t, goldenContentDigest, hex.EncodeToString(content.digest()))
}

func TestGolden_ContentDigestOfAbsentManifest(t *testing.T) {
	content, err := readStartupFixedContent(t.TempDir())
	require.NoError(t, err)
	require.Empty(t, content.coveredPaths())
}
