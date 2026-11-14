//go:build unix

package sharedserver

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const runFailureMessage = "Shared server failed"

func readRunLog(t *testing.T, logDir string) []slogtext.Record {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(logDir, testIdentity+".log"))
	require.NoError(t, err)
	records, err := slogtext.Parse(data)
	require.NoError(t, err)
	return records
}

func errorEntries(records []slogtext.Record) []slogtext.Record {
	return slogtext.Filter(records, slogtext.Record{"level": "ERROR"})
}

func stoppedEntries(records []slogtext.Record) []slogtext.Record {
	return slogtext.Filter(records, slogtext.Record{"msg": "Shared server stopped"})
}

func requireOneRunFailureEntry(t *testing.T, logDir string, returned error) {
	t.Helper()
	records := readRunLog(t, logDir)
	failures := errorEntries(records)
	require.Len(t, failures, 1, "an error return must be logged exactly once")
	assert.Equal(t, runFailureMessage, failures[0]["msg"])
	assert.Equal(t, returned.Error(), failures[0]["error"], "the entry must carry the returned error")
	assert.NotContains(t, failures[0], "session", "the failure is the server's own entry")
	assert.Empty(t, stoppedEntries(records), "an error return logs no stopped entry")
}

func requireNormalReturnEntries(t *testing.T, logDir string) {
	t.Helper()
	records := readRunLog(t, logDir)
	assert.Len(t, stoppedEntries(records), 1, "a normal return logs one stopped entry")
	assert.Empty(t, errorEntries(records), "a normal return logs no error entry")
}

func runOnce(t *testing.T, ctx context.Context, cfg Config) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
		return nil
	}
}

func TestRun_BindFailureIsLoggedAsOneErrorEntry(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	dir := shortTempDir(t)
	t.Cleanup(func() { require.NoError(t, os.Chmod(dir, 0o700)) })
	logDir := t.TempDir()
	build, built := buildSpy(nil)

	err := runOnce(t, context.Background(), Config{
		Identity:    testIdentity,
		EndpointDir: dir,
		LogDir:      logDir,
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
	assert.False(t, *built)
	_, statErr := os.Lstat(filepath.Join(dir, testIdentity+".sock"))
	assert.ErrorIs(t, statErr, fs.ErrNotExist, "a failed bind must leave no socket")
	assertLockReleased(t, dir)
	requireOneRunFailureEntry(t, logDir, err)
}

// A non-empty directory at the socket path cannot be removed by anyone,
// root included, so the stale-socket removal fails without a permission trick.
func TestRun_StaleSocketRemovalFailureIsLoggedAsOneErrorEntry(t *testing.T) {
	dir := shortTempDir(t)
	blocker := filepath.Join(dir, testIdentity+".sock")
	require.NoError(t, os.Mkdir(blocker, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(blocker, "keep"), nil, 0o600))
	logDir := t.TempDir()
	build, built := buildSpy(nil)

	err := runOnce(t, context.Background(), Config{
		Identity:        testIdentity,
		EndpointDir:     dir,
		LogDir:          logDir,
		CurrentIdentity: currentIsTestIdentity,
		Build:           build,
		Linger:          time.Hour,
	})

	require.ErrorContains(t, err, "failed to remove stale socket")
	assert.NotErrorIs(t, err, ErrIdentityMismatch)
	assert.False(t, *built)
	assert.DirExists(t, blocker)
	assertLockReleased(t, dir)
	requireOneRunFailureEntry(t, logDir, err)
}

func TestRun_BuildFailureIsLoggedAsOneErrorEntry(t *testing.T) {
	dir := shortTempDir(t)
	logDir := t.TempDir()
	errBuild := errors.New("index build failed")
	build, built := buildSpy(errBuild)

	err := runOnce(t, context.Background(), Config{
		Identity:        testIdentity,
		EndpointDir:     dir,
		LogDir:          logDir,
		CurrentIdentity: currentIsTestIdentity,
		Build:           build,
		Linger:          time.Hour,
	})

	require.ErrorIs(t, err, errBuild)
	assert.True(t, *built)
	assertLockReleased(t, dir)
	requireOneRunFailureEntry(t, logDir, err)
}

func TestRun_SelfCheckFailureIsTheOnlyErrorEntry(t *testing.T) {
	cases := map[string]func() (string, error){
		"mismatch":     func() (string, error) { return otherTestIdentity, nil },
		"uncomputable": func() (string, error) { return "", errors.New("manifest unreadable") },
	}
	for name, current := range cases {
		t.Run(name, func(t *testing.T) {
			logDir := t.TempDir()
			build, _ := buildSpy(nil)

			err := runOnce(t, context.Background(), Config{
				Identity:        testIdentity,
				EndpointDir:     shortTempDir(t),
				LogDir:          logDir,
				CurrentIdentity: current,
				Build:           build,
				Linger:          time.Hour,
			})

			require.ErrorIs(t, err, ErrIdentityMismatch)
			records := readRunLog(t, logDir)
			failures := errorEntries(records)
			require.Len(t, failures, 1, "the self-check failure must be the only error entry")
			assert.Equal(t, "Identity self-check failed", failures[0]["msg"])
			assert.NotContains(t, failures[0], "session")
			assert.Empty(t, stoppedEntries(records))
		})
	}
}

func holdTestLock(t *testing.T, dir string) {
	t.Helper()
	lock := flock.New(lockFileIn(dir))
	locked, err := lock.TryLock()
	require.NoError(t, err)
	require.True(t, locked)
	t.Cleanup(func() { _ = lock.Unlock() })
}

func TestRun_LostLockLeavesAnExistingLogByteIdentical(t *testing.T) {
	dir := shortTempDir(t)
	logDir := t.TempDir()
	logFile := filepath.Join(logDir, testIdentity+".log")
	before := []byte("time=2026-01-01T00:00:00Z level=INFO msg=\"Earlier run\"\n")
	require.NoError(t, os.WriteFile(logFile, before, 0o600))
	holdTestLock(t, dir)
	build, _ := buildSpy(nil)

	err := runOnce(t, context.Background(), Config{
		Identity:        testIdentity,
		EndpointDir:     dir,
		LogDir:          logDir,
		LogStartup:      func(logger *slog.Logger) { logger.Info("Startup marker") },
		CurrentIdentity: currentIsTestIdentity,
		Build:           build,
		Linger:          time.Hour,
	})

	require.ErrorIs(t, err, ErrLockNotAcquired)
	after, readErr := os.ReadFile(logFile)
	require.NoError(t, readErr)
	assert.Equal(t, before, after, "a candidate that loses the lock writes nothing")
}

func TestRun_LockFailureCreatesNoLog(t *testing.T) {
	cases := map[string]struct {
		prepare func(t *testing.T, dir string)
		cause   error
	}{
		"lost lock": {prepare: holdTestLock},
		"lock I/O error": {
			prepare: func(t *testing.T, dir string) {
				if os.Geteuid() == 0 {
					t.Skip("root bypasses file permissions")
				}
				require.NoError(t, os.WriteFile(lockFileIn(dir), nil, 0o000))
				t.Cleanup(func() { require.NoError(t, os.Chmod(lockFileIn(dir), 0o600)) })
			},
			cause: fs.ErrPermission,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := shortTempDir(t)
			logDir := t.TempDir()
			tc.prepare(t, dir)
			build, _ := buildSpy(nil)

			err := runOnce(t, context.Background(), Config{
				Identity:        testIdentity,
				EndpointDir:     dir,
				LogDir:          logDir,
				CurrentIdentity: currentIsTestIdentity,
				Build:           build,
				Linger:          time.Hour,
			})

			require.ErrorIs(t, err, ErrLockNotAcquired)
			if tc.cause != nil {
				require.ErrorIs(t, err, tc.cause)
			}
			assert.NoFileExists(t, filepath.Join(logDir, testIdentity+".log"))
		})
	}
}

func TestRun_CancellationAfterTheBuildIsANormalReturn(t *testing.T) {
	logDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := runOnce(t, ctx, Config{
		Identity:        testIdentity,
		EndpointDir:     shortTempDir(t),
		LogDir:          logDir,
		CurrentIdentity: currentIsTestIdentity,
		Build: func(context.Context, *slog.Logger) (*mcp.Server, func(), error) {
			cancel()
			return mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil), nil, nil
		},
		Linger: time.Hour,
	})

	require.NoError(t, err)
	requireNormalReturnEntries(t, logDir)
}

func TestRun_CancellationDuringTheBuildIsANormalReturn(t *testing.T) {
	logDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := runOnce(t, ctx, Config{
		Identity:        testIdentity,
		EndpointDir:     shortTempDir(t),
		LogDir:          logDir,
		CurrentIdentity: currentIsTestIdentity,
		Build: func(buildCtx context.Context, _ *slog.Logger) (*mcp.Server, func(), error) {
			cancel()
			<-buildCtx.Done()
			return nil, nil, buildCtx.Err()
		},
		Linger: time.Hour,
	})

	require.NoError(t, err)
	requireNormalReturnEntries(t, logDir)
}

func TestRun_LingerExpiryIsANormalReturn(t *testing.T) {
	logDir := t.TempDir()
	build, _ := buildSpy(nil)

	err := runOnce(t, context.Background(), Config{
		Identity:        testIdentity,
		EndpointDir:     shortTempDir(t),
		LogDir:          logDir,
		CurrentIdentity: currentIsTestIdentity,
		Build:           build,
		Linger:          config.MinSharedServerLinger,
	})

	require.NoError(t, err)
	requireNormalReturnEntries(t, logDir)
}
