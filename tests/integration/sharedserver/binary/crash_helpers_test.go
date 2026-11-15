//go:build unix

package binary

import (
	"syscall"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// longLinger keeps every run alive for the whole test, so no run ends or is
// replaced except by the test's own signal.
const longLinger = "10m"

// attachedSession is a handshaken session and the run serving it.
type attachedSession struct {
	env     *testkit.BinaryEnv
	launch  *testkit.BinaryLaunch
	session string
	logPath string
	server  testkit.ServerRun
}

func startAttachedSession(t *testing.T, deadline time.Time) attachedSession {
	t.Helper()
	env := testkit.NewBinaryEnv(t, acdcBinary)
	launch := env.Start(t, sharedLaunchArgs(testkit.CreateTestContentDir(t, nil), "--shared-server-linger", longLinger)...)
	handshake(t, launch, deadline)
	session, logPath := awaitIsolatedAttach(t, env, launch, deadline)
	return attachedSession{env: env, launch: launch, session: session, logPath: logPath,
		server: awaitServerRun(t, logPath, "attached:"+session, deadline)}
}

func (s attachedSession) signalServer(t *testing.T, sig syscall.Signal) {
	t.Helper()
	require.NoError(t, s.env.SignalSharedServer(s.logPath, s.server, sig))
}

// requireNextRequestRecovers sends a request only after the proxy has
// reported the crash, so the request is held by the recovery instead of
// being in flight at the crash, and requires its success on a new run of
// the same log, reached by the session's second attach. It returns the
// crashed run as read once the new run holds the lock, when the crashed
// process can no longer write to the log.
func (s attachedSession) requireNextRequestRecovers(t *testing.T, deadline time.Time) (crashed, recovered testkit.ServerRun) {
	t.Helper()
	_, err := s.launch.AwaitStderrRecord(slogtext.Record{"level": "WARN", "msg": "Shared server connection lost, recovering"}, deadline)
	require.NoError(t, err)

	next := request(t, s.launch, deadline, 3, "tools/list", `{}`)
	require.Nil(t, next.Error, "the request after the crash fails")
	assert.Contains(t, string(next.Result), `"search"`)

	session, logPath, err := s.launch.AwaitNthAttach(2, deadline)
	require.NoError(t, err)
	require.Equal(t, s.logPath, logPath)
	_, err = s.launch.AwaitStderrRecord(slogtext.Record{"level": "INFO", "msg": "Recovered the shared server connection"}, deadline)
	require.NoError(t, err)
	recovered = awaitServerRun(t, logPath, "attached:"+session, deadline)
	require.NotEqual(t, s.server.PID, recovered.PID, "the session recovers on a new shared server")
	assert.True(t, recovered.Started.After(s.server.Started), "the recovery's run starts after the crashed one")

	runs, err := testkit.ReadServerRuns(logPath)
	require.NoError(t, err)
	for _, run := range runs {
		if run.PID == s.server.PID && run.Started.Equal(s.server.Started) {
			return run, recovered
		}
	}
	require.FailNow(t, "the crashed run is gone from the log")
	return testkit.ServerRun{}, recovered
}
