package resources

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestFingerprint_StableAcrossRepeatedCallsWithNoChange(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/guide.md", "# Guide\n")
	cp := content.NewContentProvider(root)
	index := &domain.IndexMetadata{Include: []string{"docs/**/*.md"}}

	first, err := Fingerprint(context.Background(), cp, index)
	require.NoError(t, err)
	second, err := Fingerprint(context.Background(), cp, index)
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.NotEmpty(t, first)
}

func TestFingerprint_ChangesWithSelectedFileSetOrContentSize(t *testing.T) {
	cp := func(root string) *content.ContentProvider { return content.NewContentProvider(root) }
	index := &domain.IndexMetadata{Include: []string{"docs/**/*.md"}}

	t.Run("a matching file is added", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "docs/guide.md", "# Guide\n")
		before, err := Fingerprint(context.Background(), cp(root), index)
		require.NoError(t, err)

		writeFile(t, root, "docs/extra.md", "# Extra\n")
		after, err := Fingerprint(context.Background(), cp(root), index)
		require.NoError(t, err)

		require.NotEqual(t, before, after)
	})

	t.Run("a matching file's content changes size", func(t *testing.T) {
		root := t.TempDir()
		path := writeFile(t, root, "docs/guide.md", "# Guide\n")
		before, err := Fingerprint(context.Background(), cp(root), index)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(path, []byte("# Guide\n\nMore body text.\n"), 0o600))
		after, err := Fingerprint(context.Background(), cp(root), index)
		require.NoError(t, err)

		require.NotEqual(t, before, after)
	})

	t.Run("a matching file is deleted", func(t *testing.T) {
		root := t.TempDir()
		path := writeFile(t, root, "docs/guide.md", "# Guide\n")
		writeFile(t, root, "docs/other.md", "# Other\n")
		before, err := Fingerprint(context.Background(), cp(root), index)
		require.NoError(t, err)

		require.NoError(t, os.Remove(path))
		after, err := Fingerprint(context.Background(), cp(root), index)
		require.NoError(t, err)

		require.NotEqual(t, before, after)
	})

	t.Run("a matching file is renamed with identical content", func(t *testing.T) {
		root := t.TempDir()
		path := writeFile(t, root, "docs/guide.md", "# Guide\n")
		before, err := Fingerprint(context.Background(), cp(root), index)
		require.NoError(t, err)

		require.NoError(t, os.Rename(path, filepath.Join(root, "docs", "renamed.md")))
		after, err := Fingerprint(context.Background(), cp(root), index)
		require.NoError(t, err)

		require.NotEqual(t, before, after)
	})
}

// A pure timestamp touch with unchanged content still moves the digest. This
// is the documented cost of a read-free gate: Fingerprint never reads file
// contents, so modification time is the only signal it has for "this file's
// bytes might differ." A later, content-aware digest is what would suppress
// the resulting needless rebuild.
func TestFingerprint_ChangesOnTimestampTouchWithIdenticalContent(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, root, "docs/guide.md", "# Guide\n")
	cp := content.NewContentProvider(root)
	index := &domain.IndexMetadata{Include: []string{"docs/**/*.md"}}

	before, err := Fingerprint(context.Background(), cp, index)
	require.NoError(t, err)

	touched := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(path, touched, touched))

	after, err := Fingerprint(context.Background(), cp, index)
	require.NoError(t, err)

	require.NotEqual(t, before, after)
}

// These are the two cases that prove Fingerprint reuses Discover's own
// selection predicate rather than walking the whole tree: a file outside the
// include patterns, and a file under a directory the lenient policy prunes,
// must never move the digest.
func TestFingerprint_UnchangedForFilesOutsideSelection(t *testing.T) {
	t.Run("a non-matching file is added", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "docs/guide.md", "# Guide\n")
		cp := content.NewContentProvider(root)
		index := &domain.IndexMetadata{Include: []string{"docs/**/*.md"}}

		before, err := Fingerprint(context.Background(), cp, index)
		require.NoError(t, err)

		writeFile(t, root, "CONTRIBUTING.md", "# Contributing\n")
		writeFile(t, root, "src/notes.md", "# Notes\n")
		after, err := Fingerprint(context.Background(), cp, index)
		require.NoError(t, err)

		require.Equal(t, before, after)
	})

	t.Run("a file appears under a lenient-pruned directory", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "docs/guide.md", "# Guide\n")
		cp := content.NewContentProvider(root)
		index := &domain.IndexMetadata{Include: []string{"**/*.md"}}

		before, err := Fingerprint(context.Background(), cp, index, WithLenientIndex())
		require.NoError(t, err)

		writeFile(t, root, "node_modules/pkg/readme.md", "# Vendored\n")
		after, err := Fingerprint(context.Background(), cp, index, WithLenientIndex())
		require.NoError(t, err)

		require.Equal(t, before, after)
	})
}

func TestFingerprint_LegacyMode(t *testing.T) {
	t.Run("digests mcp-resources", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "mcp-resources/guide.md", "# Guide\n")
		cp := content.NewContentProvider(root)

		before, err := Fingerprint(context.Background(), cp, nil)
		require.NoError(t, err)

		writeFile(t, root, "mcp-resources/extra.md", "# Extra\n")
		after, err := Fingerprint(context.Background(), cp, nil)
		require.NoError(t, err)

		require.NotEqual(t, before, after)
	})

	t.Run("a missing resources directory yields the stable empty digest with no error", func(t *testing.T) {
		root := t.TempDir()
		cp := content.NewContentProvider(root)

		digest, err := Fingerprint(context.Background(), cp, nil)
		require.NoError(t, err)
		require.NotEmpty(t, digest)

		emptyDigestAgain, err := Fingerprint(context.Background(), cp, nil)
		require.NoError(t, err)
		require.Equal(t, digest, emptyDigestAgain)
	})
}

func TestFingerprint_EmptySelectionIsNotTheEmptyString(t *testing.T) {
	root := t.TempDir()
	cp := content.NewContentProvider(root)

	digest, err := Fingerprint(context.Background(), cp, nil)
	require.NoError(t, err)
	require.NotEqual(t, "", digest)
}

// An invalid include pattern is a configuration error, not an empty
// selection: Fingerprint must propagate it rather than digesting whatever
// happened to already match. Pattern validation runs before any filesystem
// access, so no content root is needed to observe it.
func TestFingerprint_ConfiguredInvalidPatternReturnsError(t *testing.T) {
	cp := content.NewContentProvider("")
	index := &domain.IndexMetadata{Include: []string{"/abs/pattern/**"}}

	_, err := Fingerprint(context.Background(), cp, index)

	require.Error(t, err)
	require.ErrorContains(t, err, "index pattern must be relative")
}

func TestFingerprint_ConfiguredCancelledContextReturnsError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/guide.md", "# Guide\n")
	cp := content.NewContentProvider(root)
	index := &domain.IndexMetadata{Include: []string{"docs/**/*.md"}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Fingerprint(ctx, cp, index)

	require.ErrorIs(t, err, context.Canceled)
}

// The legacy walk observes cancellation from inside its per-entry callback,
// so the mcp-resources fixture must contain at least one file for the walk
// to reach that check rather than exiting beforehand for an unrelated reason.
func TestFingerprint_LegacyCancelledContextReturnsError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "mcp-resources/guide.md", "# Guide\n")
	cp := content.NewContentProvider(root)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Fingerprint(ctx, cp, nil)

	require.ErrorIs(t, err, context.Canceled)
}
