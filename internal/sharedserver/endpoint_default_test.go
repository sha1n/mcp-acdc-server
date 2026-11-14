package sharedserver

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultEndpointDir_UsesTheHostPlatform(t *testing.T) {
	switch runtime.GOOS {
	case "linux":
		t.Run("XDG_RUNTIME_DIR set", func(t *testing.T) {
			runtimeDir := t.TempDir()
			t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

			dir, err := DefaultEndpointDir()

			require.NoError(t, err)
			assert.Equal(t, filepath.Join(runtimeDir, "acdc-mcp"), dir)
		})
		t.Run("XDG_RUNTIME_DIR unset", func(t *testing.T) {
			t.Setenv("XDG_RUNTIME_DIR", "")
			tempDir := t.TempDir()
			t.Setenv("TMPDIR", tempDir)

			dir, err := DefaultEndpointDir()

			require.NoError(t, err)
			assert.Equal(t, filepath.Join(tempDir, "acdc-mcp-"+strconv.Itoa(os.Getuid())), dir)
		})
	case "darwin":
		t.Run("HOME set", func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)

			dir, err := DefaultEndpointDir()

			require.NoError(t, err)
			assert.Equal(t, filepath.Join(home, "Library", "Caches", "acdc-mcp"), dir)
		})
		t.Run("HOME unset", func(t *testing.T) {
			t.Setenv("HOME", "")

			_, err := DefaultEndpointDir()

			require.Error(t, err)
			assert.Contains(t, err.Error(), "failed to resolve endpoint directory")
		})
	default:
		_, err := DefaultEndpointDir()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "shared server is not supported on "+runtime.GOOS)
	}
}
