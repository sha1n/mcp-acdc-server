package sharedserver

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultLogDir_UsesTheHostPlatform(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	switch runtime.GOOS {
	case "linux":
		t.Run("XDG_STATE_HOME set", func(t *testing.T) {
			stateHome := t.TempDir()
			t.Setenv("XDG_STATE_HOME", stateHome)

			dir, err := DefaultLogDir()

			require.NoError(t, err)
			assert.Equal(t, filepath.Join(stateHome, "acdc-mcp"), dir)
		})
		t.Run("XDG_STATE_HOME unset", func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", "")

			dir, err := DefaultLogDir()

			require.NoError(t, err)
			assert.Equal(t, filepath.Join(home, ".local", "state", "acdc-mcp"), dir)
		})
	case "darwin":
		dir, err := DefaultLogDir()

		require.NoError(t, err)
		assert.Equal(t, filepath.Join(home, "Library", "Logs", "acdc-mcp"), dir)
	default:
		_, err := DefaultLogDir()

		assert.Error(t, err, "no log directory outside Linux and macOS")
	}
}
