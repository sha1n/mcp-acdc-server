//go:build unix

package endpoint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shortTempDir lives under /tmp because the per-test directory of t.TempDir
// can push a socket path past the darwin sun_path limit.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "acdc-ep-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// chdirToEmptyDir moves the test into an empty working directory, so a
// relative endpoint directory that slips through cannot touch the repository
// and anything it creates is observable.
func chdirToEmptyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "nothing may be created for a non-absolute endpoint directory")
}

func TestSocketPath_NamesTheSocketAfterTheIdentity(t *testing.T) {
	endpoint, dir := openTestEndpoint(t)
	path, err := endpoint.SocketPath(testIdentity)
	require.NoError(t, err)
	lockPath, err := endpoint.LockPath(testIdentity)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dir, testIdentity+".sock"), path)
	assert.Equal(t, filepath.Join(dir, testIdentity+".lock"), lockPath)
}

func TestSocketPath_RejectsOverLongPath(t *testing.T) {
	endpoint, err := OpenEndpoint(filepath.Join(shortTempDir(t), strings.Repeat("d", 200)))
	require.NoError(t, err)

	_, err = endpoint.SocketPath(testIdentity)

	assert.ErrorIs(t, err, ErrSocketPathTooLong)
}

func TestEnsureEndpointDir_RejectsANonAbsoluteDir(t *testing.T) {
	for _, dir := range []string{"@runtime/acdc-mcp", "runtime/acdc-mcp", ""} {
		t.Run(fmt.Sprintf("%q", dir), func(t *testing.T) {
			cwd := chdirToEmptyDir(t)

			_, err := OpenEndpoint(dir)

			require.Error(t, err)
			assert.Contains(t, err.Error(), fmt.Sprintf("endpoint directory %q is not an absolute path", dir))
			assertDirEmpty(t, cwd)
		})
	}
}
