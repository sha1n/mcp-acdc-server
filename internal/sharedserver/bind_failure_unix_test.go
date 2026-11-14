//go:build unix

package sharedserver

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The self-check is the last caller code to run before bind, so revoking
// write permission on the endpoint directory there makes the bind itself
// fail: the stale-socket removal of a missing path succeeds regardless.
func TestRun_BindFailureReturnsItsCauseAndLeavesNoSocketOrBuild(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	dir := shortTempDir(t)
	t.Cleanup(func() { require.NoError(t, os.Chmod(dir, 0o700)) })
	logDir := t.TempDir()
	build, built := buildSpy(nil)

	err := Run(context.Background(), Config{
		Identity:    testIdentity,
		EndpointDir: dir,
		LogDir:      logDir,
		LogStartup:  func(logger *slog.Logger) { logger.Info("Startup marker") },
		CurrentIdentity: func() (string, error) {
			if err := os.Chmod(dir, 0o500); err != nil {
				return "", err
			}
			return testIdentity, nil
		},
		Build:  build,
		Linger: time.Hour,
	})

	require.ErrorContains(t, err, "failed to bind shared server socket")
	require.ErrorIs(t, err, fs.ErrPermission)
	assert.NotErrorIs(t, err, ErrIdentityMismatch, "the self-check passed")
	assert.False(t, *built, "no catalog may be built without a bound socket")
	_, statErr := os.Lstat(filepath.Join(dir, testIdentity+".sock"))
	assert.ErrorIs(t, statErr, fs.ErrNotExist, "a failed bind must leave no socket")
	assert.FileExists(t, lockFileIn(dir), "the lock file is never unlinked")
	assertLockReleased(t, dir)

	data, err := os.ReadFile(filepath.Join(logDir, testIdentity+".log"))
	require.NoError(t, err)
	records, err := slogtext.Parse(data)
	require.NoError(t, err)
	assert.Len(t, slogtext.Filter(records, slogtext.Record{"msg": "Startup marker"}), 1, "the log is open before bind")
	assert.Empty(t, slogtext.Filter(records, slogtext.Record{"msg": "Shared server stopped"}), "an error return logs no stopped entry")
}
