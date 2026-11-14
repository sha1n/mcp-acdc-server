//go:build unix

package identity

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Without search permission on the working directory os.Getwd cannot stat
// ".", so a relative content dir cannot be made absolute. Windows has no
// equivalent mode.
func TestCompute_RelativeContentDirInAnUnsearchableWorkingDirectoryFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory search permission")
	}
	s := baseSettings(t)
	cwd := filepath.Join(t.TempDir(), "cwd")
	require.NoError(t, os.Mkdir(cwd, 0o700))
	t.Chdir(cwd)
	require.NoError(t, os.Chmod(cwd, 0o600))
	t.Cleanup(func() { require.NoError(t, os.Chmod(cwd, 0o700)) })
	s.ContentDir = "content"

	id, err := Compute(s, stampedBinary())

	require.ErrorContains(t, err, `resolve content directory "content"`)
	require.ErrorIs(t, err, fs.ErrPermission)
	require.Empty(t, id)
}
