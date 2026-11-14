package identity

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/stretchr/testify/require"
)

// The golden values below are bound to schemaVersion. A change to
// canonicalisation, the exclusion list, the digest coverage, the absent
// manifest marker, the hash or its truncation must fail one of these tests;
// fix it by bumping schemaVersion together with the golden values, never by
// updating the values alone.
const (
	goldenSchemaVersion     = 1
	goldenStampedIdentity   = "7cc5cf1536ef837818ad530ea570045c"
	goldenUnstampedIdentity = "7d5dec57db740565559f7ad370ef118b"
	goldenContentDigest     = "b3fc12aa04a4bc707b5096b994b53cb7b9a1081ebf511bc5cbc19605b6e5daaa"
)

// goldenContentDirSentinel stands in for the resolved temporary content dir,
// whose path differs on every run, so the golden values stay hermetic.
const goldenContentDirSentinel = "/golden/content"

func goldenSettings(contentDir string) *config.Settings {
	return &config.Settings{
		ContentDir: contentDir,
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

func lengthPrefixed(fields ...string) []byte {
	var buf []byte
	for _, f := range fields {
		buf = binary.AppendUvarint(buf, uint64(len(f)))
		buf = append(buf, f...)
	}
	return buf
}

func truncatedSHA256(input []byte) string {
	sum := sha256.Sum256(input)
	return hex.EncodeToString(sum[:16])
}

// hermeticIdentity checks that Compute is the truncated SHA-256 of the
// canonical input, then returns the identity that input would have with the
// content dir record set to goldenContentDirSentinel.
func hermeticIdentity(t *testing.T, s *config.Settings, bin Binary) string {
	t.Helper()
	input, err := canonicalInput(s, bin)
	require.NoError(t, err)
	require.Equal(t, truncatedSHA256(input), mustCompute(t, s, bin))

	resolved, err := filepath.EvalSymlinks(s.ContentDir)
	require.NoError(t, err)
	record := lengthPrefixed("setting:content_dir", resolved)
	require.Equal(t, 1, bytes.Count(input, record), "content dir record must appear exactly once")
	hermetic := bytes.Replace(input, record, lengthPrefixed("setting:content_dir", goldenContentDirSentinel), 1)
	return truncatedSHA256(hermetic)
}

func writeGoldenContent(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, ".acdc", "config.yaml"), "server:\n  name: golden\n")
	writeFile(t, filepath.Join(root, ".acdc", "prompts", "b.md"), "---\nname: b\n---\nB")
	writeFile(t, filepath.Join(root, ".acdc", "prompts", "a.md"), "---\nname: a\n---\nA")
	writeFile(t, filepath.Join(root, ".acdc", "prompts", "nested", "c.md"), "C")
	writeFile(t, filepath.Join(root, ".acdc", "prompts", "notes.txt"), "ignored")
	writeFile(t, filepath.Join(root, ".acdc", "prompts", "upper.MD"), "ignored")
	writeFile(t, filepath.Join(root, ".acdc", "resources", "r.md"), "ignored")
	writeFile(t, filepath.Join(root, ".acdc", "other.yaml"), "ignored")
	writeFile(t, filepath.Join(root, "doc.md"), "ignored")
}

func TestGolden_SchemaVersion(t *testing.T) {
	require.Equal(t, goldenSchemaVersion, schemaVersion)
}

func TestGolden_ExclusionList(t *testing.T) {
	require.ElementsMatch(t, excludedSettingPaths, excludedSettings)
}

func TestGolden_StampedIdentityWithAbsentManifest(t *testing.T) {
	id := hermeticIdentity(t, goldenSettings(t.TempDir()), Binary{Version: "1.2.3", Build: "abc123"})
	require.Equal(t, goldenStampedIdentity, id)
}

func TestGolden_UnstampedIdentityWithStartupFixedContent(t *testing.T) {
	root := t.TempDir()
	writeGoldenContent(t, root)
	exe := filepath.Join(t.TempDir(), "acdc-mcp")
	writeFile(t, exe, "golden executable bytes")

	id := hermeticIdentity(t, goldenSettings(root), Binary{Version: "dev", Build: "unknown", Executable: exe})

	require.Equal(t, goldenUnstampedIdentity, id)
}

func TestGolden_ContentDigestCoverage(t *testing.T) {
	root := t.TempDir()
	writeGoldenContent(t, root)

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
