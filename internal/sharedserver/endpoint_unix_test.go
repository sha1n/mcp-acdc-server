//go:build unix

package sharedserver

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureEndpointDir_CreatesAMissingDirectoryWithMode0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "parent", "acdc-mcp")

	_, err := OpenEndpoint(dir)
	require.NoError(t, err)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	assert.Equal(t, fs.FileMode(0o700), info.Mode().Perm())
}

func TestEnsureEndpointDir_AcceptsAnExistingPrivateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acdc-mcp")
	require.NoError(t, os.Mkdir(dir, 0o700))

	_, err := OpenEndpoint(dir)

	assert.NoError(t, err)
}

func TestEnsureEndpointDir_RejectsAnExistingDirectoryWithMode0755(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acdc-mcp")
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.Chmod(dir, 0o755))

	_, err := OpenEndpoint(dir)
	assert.Error(t, err)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o755), info.Mode().Perm(), "the check must not repair the mode")
}

func TestEnsureEndpointDir_RejectsANonDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acdc-mcp")
	require.NoError(t, os.WriteFile(path, nil, 0o600))

	_, err := OpenEndpoint(path)

	assert.Error(t, err)
}

type fakeDirInfo struct {
	mode fs.FileMode
	uid  uint32
}

func (f fakeDirInfo) Name() string       { return "acdc-mcp" }
func (f fakeDirInfo) Size() int64        { return 0 }
func (f fakeDirInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeDirInfo) ModTime() time.Time { return time.Time{} }
func (f fakeDirInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeDirInfo) Sys() any           { return &syscall.Stat_t{Uid: f.uid} }

func TestVerifyEndpointDir_OwnerAndMode(t *testing.T) {
	const uid = 1000

	tests := []struct {
		name    string
		info    fs.FileInfo
		wantErr bool
	}{
		{name: "owned private directory", info: fakeDirInfo{mode: fs.ModeDir | 0o700, uid: uid}},
		{name: "another owner", info: fakeDirInfo{mode: fs.ModeDir | 0o700, uid: uid + 1}, wantErr: true},
		{name: "group readable", info: fakeDirInfo{mode: fs.ModeDir | 0o750, uid: uid}, wantErr: true},
		{name: "world readable", info: fakeDirInfo{mode: fs.ModeDir | 0o755, uid: uid}, wantErr: true},
		{name: "symlink", info: fakeDirInfo{mode: fs.ModeSymlink | 0o700, uid: uid}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyEndpointDir(tt.info, uid)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

type foreignDirInfo struct{ fakeDirInfo }

func (foreignDirInfo) Sys() any { return nil }

func TestVerifyEndpointDir_RejectsADirectoryWithoutOwnerInformation(t *testing.T) {
	info := foreignDirInfo{fakeDirInfo{mode: fs.ModeDir | 0o700}}

	err := verifyEndpointDir(info, 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "owner cannot be determined")
}
