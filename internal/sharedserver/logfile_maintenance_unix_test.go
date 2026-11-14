//go:build unix

package sharedserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenLog_UnlistableDirectoryStillOpensTheLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "write-only")
	require.NoError(t, os.Mkdir(dir, 0o300))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	file, issues := openLog(dir, testIdentity)

	require.NotNil(t, file, "a retention failure must not cost the log")
	t.Cleanup(func() { _ = file.Close() })
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0].Error(), "failed to list log directory")
}

func TestOpenLog_UndeletableStaleLogStillOpensTheLog(t *testing.T) {
	dir := t.TempDir()
	writeLogTestFile(t, filepath.Join(dir, testIdentity+".log"), 1, 0)
	stale := filepath.Join(dir, otherLogTestIdentity+".log")
	writeLogTestFile(t, stale, 1, 8*24*time.Hour)
	// The existing log stays appendable in a directory whose entries can no
	// longer be removed.
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	file, issues := openLog(dir, testIdentity)

	require.NotNil(t, file)
	t.Cleanup(func() { _ = file.Close() })
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0].Error(), "failed to delete stale log")
	assert.FileExists(t, stale)
}

func TestOpenLog_UnsearchableDirectoryReportsEveryFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "unsearchable")
	require.NoError(t, os.Mkdir(dir, 0o700))
	writeLogTestFile(t, filepath.Join(dir, otherLogTestIdentity+".log"), 1, 8*24*time.Hour)
	require.NoError(t, os.Chmod(dir, 0o600))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	file, issues := openLog(dir, testIdentity)

	assert.Nil(t, file)
	require.Len(t, issues, 2)
	assert.Contains(t, issues[0].Error(), "failed to inspect log for rotation")
	assert.Contains(t, issues[1].Error(), "failed to open log file")
}

func TestOpenLog_MissingAncestorThatCannotBeCreatedYieldsNoFile(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "readonly")
	require.NoError(t, os.Mkdir(parent, 0o500))
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	file, issues := openLog(filepath.Join(parent, "state", "acdc-mcp"), testIdentity)

	assert.Nil(t, file)
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0].Error(), "failed to create log directory")
	entries, err := os.ReadDir(parent)
	require.NoError(t, err)
	assert.Empty(t, entries)
}
