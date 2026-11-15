//go:build unix

package binary

import (
	"slices"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A server run that expired before the launch attached, and the run of a
// later candidate, are both this launch's: every run must stop, and only the
// run that served the session is bound to the moment stdin closed.
func TestBinary_SharedServerExitsAfterTheLingerAndRemovesItsOnDiskIndex(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	env := testkit.NewBinaryEnv(t, acdcBinary)
	contentDir := testkit.CreateTestContentDir(t, nil)
	launch := env.Start(t, sharedLaunchArgs(contentDir, "--shared-server-linger", "1s", "--search-in-memory=false")...)
	handshake(t, launch, deadline)
	session, logPath := awaitIsolatedAttach(t, env, launch, deadline)
	require.NotEmpty(t, searchIndexDirs(t, env), "precondition: the attached server keeps its index on disk")

	// The log's times are truncated to the millisecond at most, so the
	// reading is too.
	beforeStdinClosed := time.Now().Round(0).Truncate(time.Millisecond)
	endSession(t, launch, deadline)
	require.NoError(t, testkit.AwaitNoProcessWithArg(contentDir, deadline))

	runs, err := testkit.ReadServerRuns(logPath)
	require.NoError(t, err)
	served := slices.IndexFunc(runs, func(run testkit.ServerRun) bool {
		return slices.Contains(run.Sessions, "attached:"+session)
	})
	require.GreaterOrEqual(t, served, 0, "no server run served session %s", session)
	for _, run := range runs {
		assert.False(t, run.Stopped.IsZero(), "server run %d has no stopped entry", run.PID)
		assert.Equal(t, time.Second, run.Linger, "server run %d", run.PID)
	}
	expired := runs[served].LingerExpired
	assert.False(t, expired.Before(beforeStdinClosed.Add(time.Second)),
		"the serving run expired at %s, less than its linger after stdin closed at %s", expired, beforeStdinClosed)
	assert.Empty(t, searchIndexDirs(t, env))
}

func TestBinary_InvalidAuthFailsTheLaunchBeforeAnyDiscovery(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	env := testkit.NewBinaryEnv(t, acdcBinary)
	launch := env.Start(t, sharedLaunchArgs(testkit.CreateTestContentDir(t, nil), "--auth-type", "basic", "--auth-basic-username", "u")...)

	code, err := launch.Wait(deadline)
	require.NoError(t, err)

	assert.Equal(t, 1, code)
	assert.Contains(t, launch.Stderr(), "auth-type 'basic' requires both username and password")
	requireAbsent(t, env.EndpointDir())
	requireAbsent(t, env.LogDir())
}

func TestBinary_ALaterSessionsLingerIsIgnoredByTheRunningSharedServer(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	env := testkit.NewBinaryEnv(t, acdcBinary)
	contentDir := testkit.CreateTestContentDir(t, nil)
	first := env.Start(t, sharedLaunchArgs(contentDir, "--shared-server-linger", "1s")...)
	handshake(t, first, deadline)
	firstSession, firstLog := awaitIsolatedAttach(t, env, first, deadline)

	second := env.Start(t, sharedLaunchArgs(contentDir, "--shared-server-linger", "10m")...)
	handshake(t, second, deadline)
	secondSession, secondLog := awaitIsolatedAttach(t, env, second, deadline)
	require.Equal(t, firstLog, secondLog)
	run := awaitServerRun(t, secondLog, "attached:"+secondSession, deadline)
	require.Contains(t, run.Sessions, "attached:"+firstSession, "the second session attaches to the server that serves the first")

	endSession(t, second, deadline)
	endSession(t, first, deadline)

	stopped := awaitServerStopped(t, firstLog, run.PID, deadline)
	assert.Equal(t, time.Second, stopped.Linger, "the server lingers for its spawning session's value")
	assert.False(t, stopped.LingerExpired.IsZero())
}
