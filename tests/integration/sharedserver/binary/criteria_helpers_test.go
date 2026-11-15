//go:build unix

package binary

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// awaitIsolatedAttach returns the launch's attach line and requires its log
// to be the isolated environment's, never a developer's.
func awaitIsolatedAttach(t *testing.T, env *testkit.BinaryEnv, l *testkit.BinaryLaunch, deadline time.Time) (session, logPath string) {
	t.Helper()
	session, logPath, err := l.AwaitAttach(deadline)
	require.NoError(t, err)
	assert.Equal(t, env.LogDir(), filepath.Dir(logPath))
	assert.True(t, strings.HasPrefix(logPath, env.Root+string(filepath.Separator)), "log %s is outside %s", logPath, env.Root)
	return session, logPath
}

// awaitServerStopped polls the log until the run of pid has its stopped entry.
func awaitServerStopped(t *testing.T, logPath string, pid int, deadline time.Time) testkit.ServerRun {
	t.Helper()
	for {
		runs, err := testkit.ReadServerRuns(logPath)
		require.NoError(t, err)
		for _, run := range runs {
			if run.PID == pid && !run.Stopped.IsZero() {
				return run
			}
		}
		require.True(t, time.Now().Before(deadline), "shared server %d of %s did not stop before the deadline", pid, logPath)
		time.Sleep(20 * time.Millisecond)
	}
}

func searchIndexDirs(t *testing.T, env *testkit.BinaryEnv) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(env.TempDir(), "acdc_search_*"))
	require.NoError(t, err)
	return dirs
}

func requireAbsent(t *testing.T, path string) {
	t.Helper()
	_, err := os.Lstat(path)
	require.True(t, errors.Is(err, fs.ErrNotExist), "%s exists (lstat error: %v)", path, err)
}

func stderrLinesWithPrefix(stderr, prefix string) []string {
	var lines []string
	for line := range strings.Lines(stderr) {
		if strings.HasPrefix(line, prefix) {
			lines = append(lines, strings.TrimSuffix(line, "\n"))
		}
	}
	return lines
}
