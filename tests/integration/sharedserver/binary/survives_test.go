//go:build unix

package binary

import (
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// awaitServerRun polls the log until a run contains entry and returns it.
func awaitServerRun(t *testing.T, logPath, entry string, deadline time.Time) testkit.ServerRun {
	t.Helper()
	for {
		runs, err := testkit.ReadServerRuns(logPath)
		require.NoError(t, err)
		for _, run := range runs {
			if slices.Contains(run.Sessions, entry) {
				return run
			}
		}
		require.True(t, time.Now().Before(deadline), "no %q entry in %s before the deadline", entry, logPath)
		time.Sleep(20 * time.Millisecond)
	}
}

func TestBinary_SharedServerSurvivesTheLaunchingSessionAndASecondLaunchAttaches(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	env := testkit.NewBinaryEnv(t, acdcBinary)
	args := sharedLaunchArgs(testkit.CreateTestContentDir(t, nil), "--shared-server-linger", "10m")

	first := env.Start(t, args...)
	handshake(t, first, deadline)
	firstSession, firstLog, err := first.AwaitAttach(deadline)
	require.NoError(t, err)
	assert.Equal(t, env.LogDir(), filepath.Dir(firstLog))
	endSession(t, first, deadline)
	spawner := awaitServerRun(t, firstLog, "detached:"+firstSession, deadline)

	second := env.Start(t, args...)
	handshake(t, second, deadline)
	secondSession, secondLog, err := second.AwaitAttach(deadline)
	require.NoError(t, err)

	require.Equal(t, firstLog, secondLog)
	served := awaitServerRun(t, secondLog, "attached:"+secondSession, deadline)
	assert.Equal(t, spawner.PID, served.PID, "the second launch attaches to the server the first one spawned")
	assert.Contains(t, served.Sessions, "attached:"+firstSession)
	assert.True(t, served.Stopped.IsZero())
	assert.NoError(t, syscall.Kill(served.PID, 0), "the shared server outlives its spawning session")
	endSession(t, second, deadline)
}

func TestBinary_LeakGuardKillsALiveSharedServer(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	env := testkit.NewBinaryEnv(t, acdcBinary)
	launch := env.Start(t, sharedLaunchArgs(testkit.CreateTestContentDir(t, nil), "--shared-server-linger", "10m")...)
	handshake(t, launch, deadline)
	session, logPath, err := launch.AwaitAttach(deadline)
	require.NoError(t, err)
	assert.Equal(t, env.LogDir(), filepath.Dir(logPath))
	endSession(t, launch, deadline)
	run := awaitServerRun(t, logPath, "attached:"+session, deadline)
	require.NoError(t, syscall.Kill(run.PID, 0), "precondition: the shared server is alive")

	env.CheckSharedServerLeaks(t)

	ep, err := endpoint.OpenEndpoint(env.EndpointDir())
	require.NoError(t, err)
	lockPath, err := ep.LockPath(strings.TrimSuffix(filepath.Base(logPath), ".log"))
	require.NoError(t, err)
	lock := flock.New(lockPath)
	locked, err := lock.TryLock()
	require.NoError(t, err)
	assert.True(t, locked, "the guard returned before the shared server released its lock")
	require.NoError(t, lock.Unlock())

	for syscall.Kill(run.PID, 0) == nil {
		require.True(t, time.Now().Before(deadline), "the guard did not kill shared server %d", run.PID)
		time.Sleep(20 * time.Millisecond)
	}
}
