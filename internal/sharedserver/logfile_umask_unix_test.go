//go:build unix

package sharedserver

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setLogTestUmask changes the process-wide umask, so callers must not run in
// parallel and must obtain their temporary directories before calling it.
func setLogTestUmask(t *testing.T, mask int) {
	t.Helper()
	previous := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(previous) })
}

func TestOpenLog_NewFileIs0600UnderARestrictiveUmask(t *testing.T) {
	dir := t.TempDir()
	setLogTestUmask(t, 0o200)

	file, issues := openLog(dir, testIdentity)

	require.NotNil(t, file, "issues: %v", issues)
	assert.Empty(t, issues)
	info, err := file.Stat()
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	require.NoError(t, file.Close())

	reopened, issues := openLog(dir, testIdentity)
	require.NotNil(t, reopened, "a restarted shared server must be able to append: %v", issues)
	require.NoError(t, reopened.Close())
}

func TestOpenLog_CreatedDirectoriesAre0700UnderARestrictiveUmask(t *testing.T) {
	for _, mask := range []int{0o077, 0o277, 0o777} {
		t.Run(os.FileMode(mask).String(), func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), "state")
			dir := filepath.Join(parent, "acdc-mcp")
			setLogTestUmask(t, mask)

			file, issues := openLog(dir, testIdentity)

			require.NotNil(t, file, "issues: %v", issues)
			t.Cleanup(func() { _ = file.Close() })
			assert.Empty(t, issues)
			for _, created := range []string{parent, dir} {
				info, err := os.Stat(created)
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(), created)
			}
			info, err := file.Stat()
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		})
	}
}

func TestOpenLog_LeavesTheModeOfAnExistingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acdc-mcp")
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.Chmod(dir, 0o750))
	setLogTestUmask(t, 0o077)

	file, issues := openLog(dir, testIdentity)

	require.NotNil(t, file, "issues: %v", issues)
	t.Cleanup(func() { _ = file.Close() })
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o750), info.Mode().Perm())
}

func TestOpenLog_ExistingFileEndsAt0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, testIdentity+".log")
	require.NoError(t, os.WriteFile(path, []byte("earlier\n"), 0o600))
	require.NoError(t, os.Chmod(path, 0o644))

	file, issues := openLog(dir, testIdentity)

	require.NotNil(t, file, "issues: %v", issues)
	t.Cleanup(func() { _ = file.Close() })
	info, err := file.Stat()
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
