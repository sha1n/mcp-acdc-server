//go:build unix

package endpoint

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestEndpoint(t *testing.T) (Endpoint, string) {
	t.Helper()
	dir := filepath.Join(shortTempDir(t), "ep")
	endpoint, err := OpenEndpoint(dir)
	require.NoError(t, err)
	return endpoint, dir
}

func TestOpenEndpoint_ValidDirectoryYieldsTheIdentityPaths(t *testing.T) {
	endpoint, dir := openTestEndpoint(t)

	socketPath, err := endpoint.SocketPath(testIdentity)
	require.NoError(t, err)
	lockPath, err := endpoint.LockPath(testIdentity)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dir, testIdentity+".sock"), socketPath)
	assert.Equal(t, filepath.Join(dir, testIdentity+".lock"), lockPath)
}

func TestOpenEndpoint_RejectsANonAbsoluteDir(t *testing.T) {
	for _, dir := range []string{"", "runtime/acdc-mcp", "@runtime/acdc-mcp"} {
		t.Run(fmt.Sprintf("%q", dir), func(t *testing.T) {
			cwd := chdirToEmptyDir(t)

			_, err := OpenEndpoint(dir)

			require.Error(t, err)
			assert.Contains(t, err.Error(), fmt.Sprintf("endpoint directory %q is not an absolute path", dir))
			assertDirEmpty(t, cwd)
		})
	}
}

func TestOpenEndpoint_RejectsADirectoryOfAnotherOwner(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root owns the probe directory")
	}

	_, err := OpenEndpoint("/")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "owned by uid 0")
}

func TestOpenEndpoint_RejectsAModeOtherThan0700(t *testing.T) {
	for _, mode := range []fs.FileMode{0o750, 0o755, 0o600, 0o777} {
		t.Run(mode.String(), func(t *testing.T) {
			dir := filepath.Join(shortTempDir(t), "ep")
			require.NoError(t, os.Mkdir(dir, 0o700))
			require.NoError(t, os.Chmod(dir, mode))
			t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

			_, err := OpenEndpoint(dir)

			require.Error(t, err)
			assert.Contains(t, err.Error(), fmt.Sprintf("mode %#o, want 0700", mode))
		})
	}
}

// A setgid parent hands setgid to new directories on Linux, so the endpoint
// directory may carry special bits without its owner choosing them.
func TestOpenEndpoint_AcceptsSpecialModeBitsOnAPrivateDirectory(t *testing.T) {
	for _, special := range []fs.FileMode{fs.ModeSetgid, fs.ModeSticky} {
		t.Run(special.String(), func(t *testing.T) {
			dir := filepath.Join(shortTempDir(t), "ep")
			require.NoError(t, os.Mkdir(dir, 0o700))
			require.NoError(t, os.Chmod(dir, special|0o700))
			info, err := os.Lstat(dir)
			require.NoError(t, err)
			if info.Mode()&special == 0 {
				t.Skipf("the file system dropped %v", special)
			}

			endpoint, err := OpenEndpoint(dir)
			require.NoError(t, err)

			socketPath, err := endpoint.SocketPath(testIdentity)
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(dir, testIdentity+".sock"), socketPath)
		})
	}
}

func TestVerifyEndpointDir_AcceptsSpecialModeBits(t *testing.T) {
	const uid = 1000

	for _, special := range []fs.FileMode{fs.ModeSetuid, fs.ModeSetgid, fs.ModeSticky} {
		t.Run(special.String(), func(t *testing.T) {
			info := fakeDirInfo{mode: fs.ModeDir | special | 0o700, uid: uid}

			assert.NoError(t, verifyEndpointDir(info, uid))
		})
	}
}

// Paths are names, not descriptors: a directory replaced after verification
// is used as found, because only its owner or root can replace it.
func TestEndpoint_PathsAreResolvedByNameNotAnchoredToTheVerifiedDirectory(t *testing.T) {
	endpoint, dir := openTestEndpoint(t)
	require.NoError(t, os.Rename(dir, dir+".old"))
	require.NoError(t, os.Mkdir(dir, 0o755))

	socketPath, err := endpoint.SocketPath(testIdentity)
	require.NoError(t, err)
	lockPath, err := endpoint.LockPath(testIdentity)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dir, testIdentity+".sock"), socketPath)
	assert.Equal(t, filepath.Join(dir, testIdentity+".lock"), lockPath)
}

func TestEndpoint_RejectsAnInvalidIdentity(t *testing.T) {
	endpoint, _ := openTestEndpoint(t)

	for _, identity := range []string{"", "../escape", strings.ToUpper(testIdentity), testIdentity + "0"} {
		t.Run(fmt.Sprintf("%q", identity), func(t *testing.T) {
			_, socketErr := endpoint.SocketPath(identity)
			_, lockErr := endpoint.LockPath(identity)

			wantErr := fmt.Sprintf("invalid shared server identity %q", identity)
			require.Error(t, socketErr)
			assert.Contains(t, socketErr.Error(), wantErr)
			require.Error(t, lockErr)
			assert.Contains(t, lockErr.Error(), wantErr)
		})
	}
}

func TestEndpoint_RejectsAnOverLongSocketPath(t *testing.T) {
	endpoint, err := OpenEndpoint(filepath.Join(shortTempDir(t), strings.Repeat("d", 120)))
	require.NoError(t, err)

	_, err = endpoint.SocketPath(testIdentity)

	assert.ErrorIs(t, err, ErrSocketPathTooLong)
}

func TestEndpoint_ZeroValueYieldsNoPaths(t *testing.T) {
	var endpoint Endpoint

	socketPath, socketErr := endpoint.SocketPath(testIdentity)
	lockPath, lockErr := endpoint.LockPath(testIdentity)

	assert.ErrorIs(t, socketErr, errEndpointNotOpened)
	assert.Empty(t, socketPath)
	assert.ErrorIs(t, lockErr, errEndpointNotOpened)
	assert.Empty(t, lockPath)
}
