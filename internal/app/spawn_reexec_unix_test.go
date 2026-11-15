//go:build unix

package app

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	spawnRecordEnv  = "ACDC_APP_TEST_SPAWN_RECORD"
	spawnReleaseEnv = "ACDC_APP_TEST_SPAWN_RELEASE"
	spawnMarkerEnv  = "ACDC_APP_TEST_SPAWN_MARKER"
	// spawnChildWatchdog bounds how long a spawned child waits for its
	// release, so it ends by itself even when the test never releases it.
	spawnChildWatchdog = 30 * time.Second
)

type spawnRecord struct {
	Args          []string `json:"args"`
	Executable    string   `json:"executable"`
	Dir           string   `json:"dir"`
	Marker        string   `json:"marker"`
	SessionLeader bool     `json:"session_leader"`
}

// TestMain turns a re-execution of this test binary by spawnSharedServer into
// a harmless child: it exits before the testing package runs, so it writes
// no test output and no coverage profile.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--"+sharedServerIdentityFlag {
		os.Exit(runAsSpawnedChild())
	}
	os.Exit(m.Run())
}

func runAsSpawnedChild() int {
	recordPath := os.Getenv(spawnRecordEnv)
	if recordPath == "" {
		return 0
	}
	for deadline := time.Now().Add(spawnChildWatchdog); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(os.Getenv(spawnReleaseEnv)); err == nil {
			break
		}
	}
	if err := writeSpawnRecord(recordPath); err != nil {
		return 1
	}
	return 0
}

func writeSpawnRecord(recordPath string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	sid, err := unix.Getsid(0)
	if err != nil {
		return err
	}
	data, err := json.Marshal(spawnRecord{
		Args:          os.Args,
		Executable:    executable,
		Dir:           dir,
		Marker:        os.Getenv(spawnMarkerEnv),
		SessionLeader: sid == os.Getpid(),
	})
	if err != nil {
		return err
	}
	staged := recordPath + ".tmp"
	if err := os.WriteFile(staged, data, 0o600); err != nil {
		return err
	}
	return os.Rename(staged, recordPath)
}

func TestSpawnSharedServer_ReExecutesThisLaunchDetachedWithoutWaiting(t *testing.T) {
	deadline := time.Now().Add(spawnChildWatchdog)
	dir := t.TempDir()
	recordPath := filepath.Join(dir, "record.json")
	release := filepath.Join(dir, "release")
	t.Setenv(spawnRecordEnv, recordPath)
	t.Setenv(spawnReleaseEnv, release)
	t.Setenv(spawnMarkerEnv, "inherited")
	// The child is a coverage-instrumented copy of this binary under -cover;
	// without a coverage dir it emits nothing at exit.
	t.Setenv("GOCOVERDIR", "")
	t.Cleanup(func() { releaseSpawnedChild(t, release) })
	executable, err := os.Executable()
	require.NoError(t, err)
	workingDir, err := os.Getwd()
	require.NoError(t, err)

	require.NoError(t, spawnSharedServer(spawnTestIdentity))

	_, err = os.Stat(recordPath)
	require.ErrorIs(t, err, fs.ErrNotExist, "the child writes its record only once released, so the spawn returned without waiting for it")
	releaseSpawnedChild(t, release)
	record := awaitSpawnRecord(t, recordPath, deadline)
	assert.Equal(t, append([]string{executable, "--" + sharedServerIdentityFlag, spawnTestIdentity}, os.Args[1:]...), record.Args)
	assert.Equal(t, executable, record.Executable)
	assert.Equal(t, workingDir, record.Dir)
	assert.Equal(t, "inherited", record.Marker, "the environment is inherited")
	assert.True(t, record.SessionLeader, "the child leads a session of its own")
}

func releaseSpawnedChild(t *testing.T, release string) {
	if err := os.WriteFile(release, nil, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("failed to release the spawned child: %v", err)
	}
}

func awaitSpawnRecord(t *testing.T, recordPath string, deadline time.Time) spawnRecord {
	t.Helper()
	for {
		data, err := os.ReadFile(recordPath)
		if err == nil {
			var record spawnRecord
			require.NoError(t, json.Unmarshal(data, &record))
			return record
		}
		require.ErrorIs(t, err, fs.ErrNotExist)
		require.True(t, time.Now().Before(deadline), "the spawned child wrote no record")
		time.Sleep(10 * time.Millisecond)
	}
}
