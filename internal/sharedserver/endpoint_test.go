package sharedserver

import (
	"errors"
	"path/filepath"
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
