package sharedserver

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testIdentity = "0123456789abcdef0123456789abcdef"

func fakeEnv(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestResolveEndpointDir_PerPlatform(t *testing.T) {
	cacheDir := func() (string, error) { return "/Users/u/Library/Caches", nil }
	tempDir := func() string { return "/tmp" }

	tests := []struct {
		name     string
		platform endpointPlatform
		want     string
	}{
		{
			name: "linux with XDG_RUNTIME_DIR",
			platform: endpointPlatform{
				goos:         "linux",
				getenv:       fakeEnv(map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"}),
				tempDir:      tempDir,
				userCacheDir: cacheDir,
				uid:          1000,
			},
			want: "/run/user/1000/acdc-mcp",
		},
		{
			name: "linux without XDG_RUNTIME_DIR",
			platform: endpointPlatform{
				goos:         "linux",
				getenv:       fakeEnv(nil),
				tempDir:      tempDir,
				userCacheDir: cacheDir,
				uid:          1000,
			},
			want: "/tmp/acdc-mcp-1000",
		},
		{
			name: "darwin uses the user cache dir and never TMPDIR",
			platform: endpointPlatform{
				goos:         "darwin",
				getenv:       fakeEnv(map[string]string{"TMPDIR": "/var/folders/xx/T/", "XDG_RUNTIME_DIR": "/run/user/501"}),
				tempDir:      func() string { return "/var/folders/xx/T/" },
				userCacheDir: cacheDir,
				uid:          501,
			},
			want: "/Users/u/Library/Caches/acdc-mcp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveEndpointDir(tt.platform)
			require.NoError(t, err)
			assert.Equal(t, filepath.FromSlash(tt.want), got)
		})
	}
}

func TestResolveEndpointDir_Failures(t *testing.T) {
	tests := []struct {
		name     string
		platform endpointPlatform
	}{
		{
			name: "darwin without a user cache dir",
			platform: endpointPlatform{
				goos:         "darwin",
				getenv:       fakeEnv(nil),
				tempDir:      func() string { return "/tmp" },
				userCacheDir: func() (string, error) { return "", errors.New("no home") },
			},
		},
		{
			name: "windows has no endpoint",
			platform: endpointPlatform{
				goos:         "windows",
				getenv:       fakeEnv(nil),
				tempDir:      func() string { return `C:\Temp` },
				userCacheDir: func() (string, error) { return `C:\Cache`, nil },
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveEndpointDir(tt.platform)
			assert.Error(t, err)
		})
	}
}

func socketPathOfLength(n int) string {
	return "/" + strings.Repeat("a", n-1)
}

func TestCheckSocketPathLength_PlatformLimits(t *testing.T) {
	tests := []struct {
		goos    string
		length  int
		wantErr bool
	}{
		{goos: "darwin", length: 103, wantErr: false},
		{goos: "darwin", length: 104, wantErr: true},
		{goos: "linux", length: 107, wantErr: false},
		{goos: "linux", length: 108, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			err := checkSocketPathLength(tt.goos, socketPathOfLength(tt.length))
			if tt.wantErr {
				assert.ErrorIs(t, err, ErrSocketPathTooLong)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestSocketPath_NamesTheSocketAfterTheIdentity(t *testing.T) {
	endpoint := Endpoint{dir: "/tmp/ep"}
	path, err := endpoint.SocketPath(testIdentity)
	require.NoError(t, err)
	lockPath, err := endpoint.LockPath(testIdentity)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join("/tmp/ep", testIdentity+".sock"), path)
	assert.Equal(t, filepath.Join("/tmp/ep", testIdentity+".lock"), lockPath)
}

func TestSocketPath_RejectsOverLongPath(t *testing.T) {
	_, err := Endpoint{dir: "/" + strings.Repeat("d", 200)}.SocketPath(testIdentity)

	assert.ErrorIs(t, err, ErrSocketPathTooLong)
}

func TestValidIdentity(t *testing.T) {
	tests := []struct {
		identity string
		want     bool
	}{
		{identity: testIdentity, want: true},
		{identity: strings.ToUpper(testIdentity), want: false},
		{identity: testIdentity[:31], want: false},
		{identity: testIdentity + "0", want: false},
		{identity: "../../../../etc/passwd0123456789a", want: false},
		{identity: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.identity, func(t *testing.T) {
			assert.Equal(t, tt.want, ValidIdentity(tt.identity))
		})
	}
}
