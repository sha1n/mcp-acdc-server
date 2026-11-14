//go:build unix

package sharedserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func assertRejectsNonAbsoluteEndpointDir(t *testing.T, endpointDir string) {
	t.Helper()
	cwd := chdirToEmptyDir(t)
	build, built := buildSpy(errors.New("build must not run"))

	err := Run(context.Background(), Config{Identity: testIdentity, EndpointDir: endpointDir, Build: build})

	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("endpoint directory %q is not an absolute path", endpointDir))
	assert.NotErrorIs(t, err, ErrLockNotAcquired)
	assert.False(t, *built, "nothing may be bound for a non-absolute endpoint directory")
	assertDirEmpty(t, cwd)
}

func TestRun_RejectsANonAbsoluteEndpointDir(t *testing.T) {
	tests := []struct {
		name string
		dir  string
	}{
		{name: "abstract socket prefix", dir: "@runtime/acdc-mcp"},
		{name: "relative", dir: "runtime/acdc-mcp"},
		{name: "empty", dir: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRejectsNonAbsoluteEndpointDir(t, tt.dir)
		})
	}
}

func TestRun_RejectsANonAbsoluteEndpointDirFromEveryResolverSource(t *testing.T) {
	absCacheDir := func() (string, error) { return "/Users/u/Library/Caches", nil }
	absTempDir := func() string { return "/tmp" }

	tests := []struct {
		name     string
		platform endpointPlatform
	}{
		{
			name: "linux XDG_RUNTIME_DIR with abstract socket prefix",
			platform: endpointPlatform{
				goos:         "linux",
				getenv:       fakeEnv(map[string]string{"XDG_RUNTIME_DIR": "@runtime"}),
				tempDir:      absTempDir,
				userCacheDir: absCacheDir,
				uid:          1000,
			},
		},
		{
			name: "linux relative XDG_RUNTIME_DIR",
			platform: endpointPlatform{
				goos:         "linux",
				getenv:       fakeEnv(map[string]string{"XDG_RUNTIME_DIR": "runtime"}),
				tempDir:      absTempDir,
				userCacheDir: absCacheDir,
				uid:          1000,
			},
		},
		{
			name: "linux relative temp dir fallback",
			platform: endpointPlatform{
				goos:         "linux",
				getenv:       fakeEnv(nil),
				tempDir:      func() string { return "@tmp" },
				userCacheDir: absCacheDir,
				uid:          1000,
			},
		},
		{
			name: "linux empty temp dir fallback",
			platform: endpointPlatform{
				goos:         "linux",
				getenv:       fakeEnv(nil),
				tempDir:      func() string { return "" },
				userCacheDir: absCacheDir,
				uid:          1000,
			},
		},
		{
			name: "darwin relative user cache dir",
			platform: endpointPlatform{
				goos:         "darwin",
				getenv:       fakeEnv(nil),
				tempDir:      absTempDir,
				userCacheDir: func() (string, error) { return "Library/Caches", nil },
				uid:          501,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, err := resolveEndpointDir(tt.platform)
			require.NoError(t, err)

			assertRejectsNonAbsoluteEndpointDir(t, dir)
		})
	}
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
