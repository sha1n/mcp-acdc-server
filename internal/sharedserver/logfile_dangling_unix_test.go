//go:build unix

package sharedserver

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A dangling symlink is absent to Stat but present to Mkdir, the same answer
// a concurrent creator produces, so createLogDir treats it as an existing
// directory and the failure surfaces at the open.
func TestOpenLog_DanglingSymlinkAsTheLogDirectoryFailsAtTheOpen(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "missing")
	dir := filepath.Join(parent, "logs")
	require.NoError(t, os.Symlink(target, dir))

	file, issues := openLog(dir, testIdentity)

	require.Nil(t, file)
	require.NotEmpty(t, issues)
	last := issues[len(issues)-1]
	require.ErrorContains(t, last, "failed to open log file")
	require.ErrorIs(t, last, fs.ErrNotExist)
	for _, issue := range issues {
		require.NotContains(t, issue.Error(), "failed to create log directory")
	}
	_, err := os.Lstat(target)
	require.ErrorIs(t, err, fs.ErrNotExist, "nothing is created at the symlink target")
}
