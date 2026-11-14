//go:build unix

package sharedserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const otherTestIdentity = "fedcba9876543210fedcba9876543210"

func currentIsTestIdentity() (string, error) {
	return testIdentity, nil
}

func readTestLog(t *testing.T, logDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(logDir, testIdentity+".log"))
	require.NoError(t, err)
	return string(data)
}

func TestRun_RejectsAMissingCurrentIdentityBeforeCreatingAnything(t *testing.T) {
	endpointDir := filepath.Join(shortTempDir(t), "ep")
	logDir := filepath.Join(t.TempDir(), "logs")
	build, built := buildSpy(nil)

	err := Run(context.Background(), Config{Identity: testIdentity, EndpointDir: endpointDir, LogDir: logDir, Build: build, Linger: time.Hour})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "current identity")
	assert.NoDirExists(t, endpointDir, "nothing may be created without a self-check")
	assert.NoDirExists(t, logDir, "nothing may be created without a self-check")
	assert.False(t, *built)
}

func TestRun_IdentityMismatchIsLoggedAndExitsBeforeBind(t *testing.T) {
	dir := shortTempDir(t)
	logDir := t.TempDir()
	build, built := buildSpy(nil)

	err := Run(context.Background(), Config{
		Identity:        testIdentity,
		EndpointDir:     dir,
		LogDir:          logDir,
		CurrentIdentity: func() (string, error) { return otherTestIdentity, nil },
		Build:           build,
		Linger:          time.Hour,
	})

	require.ErrorIs(t, err, ErrIdentityMismatch)
	assert.NoFileExists(t, filepath.Join(dir, testIdentity+".sock"), "a mismatched server must not bind")
	assert.False(t, *built)
	assertLockReleased(t, dir)
	logged := readTestLog(t, logDir)
	assert.Contains(t, logged, "level=ERROR")
	assert.Contains(t, logged, "Identity self-check failed")
	assert.Contains(t, logged, otherTestIdentity, "the log must name the identity of the content on disk")
}

func TestRun_UncomputableIdentityIsLoggedAndExitsBeforeBind(t *testing.T) {
	dir := shortTempDir(t)
	logDir := t.TempDir()
	errCompute := errors.New("prompt file unreadable")
	build, built := buildSpy(nil)

	err := Run(context.Background(), Config{
		Identity:        testIdentity,
		EndpointDir:     dir,
		LogDir:          logDir,
		CurrentIdentity: func() (string, error) { return "", errCompute },
		Build:           build,
		Linger:          time.Hour,
	})

	require.ErrorIs(t, err, ErrIdentityMismatch)
	require.ErrorIs(t, err, errCompute, "the cause must be preserved")
	assert.NoFileExists(t, filepath.Join(dir, testIdentity+".sock"), "a server that cannot check its identity must not bind")
	assert.False(t, *built)
	assertLockReleased(t, dir)
	logged := readTestLog(t, logDir)
	assert.Contains(t, logged, "level=ERROR")
	assert.Contains(t, logged, "Identity self-check failed")
	assert.Contains(t, logged, errCompute.Error())
}

func TestRun_SelfCheckRunsWithTheLockHeldAndTheLogOpenBeforeBind(t *testing.T) {
	dir := shortTempDir(t)
	logDir := t.TempDir()
	build, _ := buildSpy(nil)
	type observation struct {
		lockHeld  bool
		logExists bool
		socketErr error
	}
	observed := make(chan observation, 1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Identity:    testIdentity,
			EndpointDir: dir,
			LogDir:      logDir,
			CurrentIdentity: func() (string, error) {
				probe := flock.New(lockFileIn(dir))
				locked, err := probe.TryLock()
				if locked {
					_ = probe.Unlock()
				}
				_, logErr := os.Stat(filepath.Join(logDir, testIdentity+".log"))
				_, socketErr := os.Lstat(filepath.Join(dir, testIdentity+".sock"))
				observed <- observation{lockHeld: err == nil && !locked, logExists: logErr == nil, socketErr: socketErr}
				return testIdentity, nil
			},
			Build:  build,
			Linger: time.Hour,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Error("Run did not return after cancellation")
		}
	})

	select {
	case o := <-observed:
		assert.True(t, o.lockHeld, "the self-check must run while the lock is held")
		assert.True(t, o.logExists, "the self-check must run after the log is open")
		assert.ErrorIs(t, o.socketErr, os.ErrNotExist, "the self-check must run before bind")
	case <-time.After(10 * time.Second):
		t.Fatal("the self-check did not run")
	}
}

func TestRun_LostLockSkipsTheSelfCheck(t *testing.T) {
	dir := shortTempDir(t)
	holder := flock.New(lockFileIn(dir))
	locked, err := holder.TryLock()
	require.NoError(t, err)
	require.True(t, locked)
	t.Cleanup(func() { _ = holder.Unlock() })
	checked := false
	build, _ := buildSpy(nil)

	err = Run(context.Background(), Config{
		Identity:        testIdentity,
		EndpointDir:     dir,
		LogDir:          t.TempDir(),
		CurrentIdentity: func() (string, error) { checked = true; return testIdentity, nil },
		Build:           build,
		Linger:          time.Hour,
	})

	require.ErrorIs(t, err, ErrLockNotAcquired)
	assert.False(t, checked, "a candidate that loses the lock must not read the content")
}
