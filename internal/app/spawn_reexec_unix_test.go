//go:build unix

package app

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	spawnRendezvousEnv = "ACDC_APP_TEST_SPAWN_RENDEZVOUS"
	spawnMarkerEnv     = "ACDC_APP_TEST_SPAWN_MARKER"
	// detachedTestChildArg makes this test binary a spawned child without the
	// shared server flag, for tests that start it through startDetached.
	detachedTestChildArg = "-acdc-detached-test-child"
	// spawnChildWatchdog bounds every wait of the test and of its child, so
	// the child ends by itself even when the test never releases it.
	spawnChildWatchdog = 30 * time.Second
)

type spawnRecord struct {
	PID           int      `json:"pid"`
	Args          []string `json:"args"`
	Executable    string   `json:"executable"`
	Dir           string   `json:"dir"`
	Marker        string   `json:"marker"`
	SessionLeader bool     `json:"session_leader"`
}

// TestMain turns a re-execution of this test binary into a harmless child: it
// exits before the testing package runs, so it writes no test output and no
// coverage profile.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && (os.Args[1] == "--"+sharedServerIdentityFlag || os.Args[1] == detachedTestChildArg) {
		os.Exit(runAsSpawnedChild())
	}
	os.Exit(m.Run())
}

// runAsSpawnedChild meets its test at the rendezvous socket, waits there for
// the release, answers with its record and ends. Without a rendezvous it was
// spawned by a test that does not observe it, and ends at once.
func runAsSpawnedChild() int {
	socket := os.Getenv(spawnRendezvousEnv)
	if socket == "" {
		return 0
	}
	conn, err := net.DialTimeout("unix", socket, spawnChildWatchdog)
	if err != nil {
		return 1
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(spawnChildWatchdog)); err != nil {
		return 1
	}
	if _, err := conn.Read(make([]byte, 1)); err != nil {
		return 1
	}
	record, err := spawnRecordOfThisProcess()
	if err != nil {
		return 1
	}
	if err := json.NewEncoder(conn).Encode(record); err != nil {
		return 1
	}
	return 0
}

func spawnRecordOfThisProcess() (spawnRecord, error) {
	executable, err := os.Executable()
	if err != nil {
		return spawnRecord{}, err
	}
	dir, err := os.Getwd()
	if err != nil {
		return spawnRecord{}, err
	}
	sid, err := unix.Getsid(0)
	if err != nil {
		return spawnRecord{}, err
	}
	return spawnRecord{
		PID:           os.Getpid(),
		Args:          os.Args,
		Executable:    executable,
		Dir:           dir,
		Marker:        os.Getenv(spawnMarkerEnv),
		SessionLeader: sid == os.Getpid(),
	}, nil
}

// spawnRendezvous is where a test meets the child it spawns. The child blocks
// there until released, so a start that returns before the release returned
// without waiting for the child.
type spawnRendezvous struct {
	listener *net.UnixListener
}

// newSpawnRendezvous listens on a socket every child spawned later in the test
// finds through its environment. Closing it at cleanup ends a child that is
// still waiting for its release.
func newSpawnRendezvous(t *testing.T) *spawnRendezvous {
	t.Helper()
	// The test's own temp dir can exceed the platform's socket path limit.
	dir, err := os.MkdirTemp("", "acdc-spawn-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	t.Setenv(spawnRendezvousEnv, socket)
	// A child under -cover is a coverage-instrumented copy of this binary;
	// without a coverage dir it emits nothing at exit.
	t.Setenv("GOCOVERDIR", "")
	return &spawnRendezvous{listener: listener}
}

// requireReturnsBeforeRelease runs start and requires it to return while
// its child is still held at the rendezvous.
func requireReturnsBeforeRelease(t *testing.T, deadline time.Time, start func() error) {
	t.Helper()
	started := make(chan error, 1)
	go func() { started <- start() }()
	select {
	case err := <-started:
		require.NoError(t, err)
	case <-time.After(time.Until(deadline)):
		require.FailNow(t, "the start waited for a child that runs until the test releases it")
	}
}

// release lets the child go and returns its record. It requires the launching
// process, not the test, to reap the child: a zombie still answers kill 0. It
// also requires the child to leave no process behind in its process group.
func (r *spawnRendezvous) release(t *testing.T, deadline time.Time) spawnRecord {
	t.Helper()
	require.NoError(t, r.listener.SetDeadline(deadline))
	conn, err := r.listener.Accept()
	require.NoError(t, err, "the spawned child did not reach the rendezvous")
	defer func() { _ = conn.Close() }()
	require.NoError(t, conn.SetDeadline(deadline))
	_, err = conn.Write([]byte{1})
	require.NoError(t, err)
	data, err := io.ReadAll(conn)
	require.NoError(t, err)
	var record spawnRecord
	require.NoError(t, json.Unmarshal(data, &record), string(data))
	require.Eventually(t, func() bool { return errors.Is(unix.Kill(record.PID, 0), unix.ESRCH) },
		time.Until(deadline), 10*time.Millisecond, "the spawned child %d was not reaped once it had answered", record.PID)
	assert.ErrorIs(t, unix.Kill(-record.PID, 0), unix.ESRCH, "the spawned child left a process in its group")
	return record
}

func TestSpawnSharedServer_ReExecutesThisLaunchDetachedWithoutWaiting(t *testing.T) {
	deadline := time.Now().Add(spawnChildWatchdog)
	rendezvous := newSpawnRendezvous(t)
	t.Setenv(spawnMarkerEnv, "inherited")
	executable, err := os.Executable()
	require.NoError(t, err)
	workingDir, err := os.Getwd()
	require.NoError(t, err)

	requireReturnsBeforeRelease(t, deadline, func() error { return spawnSharedServer(spawnTestIdentity) })

	record := rendezvous.release(t, deadline)
	assert.Equal(t, append([]string{executable, "--" + sharedServerIdentityFlag, spawnTestIdentity}, os.Args[1:]...), record.Args)
	assert.Equal(t, executable, record.Executable)
	assert.Equal(t, workingDir, record.Dir)
	assert.Equal(t, "inherited", record.Marker, "the environment is inherited")
	assert.True(t, record.SessionLeader, "the child leads a session of its own")
}
