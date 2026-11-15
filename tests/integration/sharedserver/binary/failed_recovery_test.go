//go:build unix

package binary

import (
	"encoding/json"
	"syscall"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The binary is replaced by a stub before the server is killed, so every
// respawn of the recovery yields no server. The identity hashes the
// executable, so the recovery dials the stub's identity, which the stub
// records; the test then ends the recovery itself by serving that socket path
// with a listener that drops every connection before the ready line. The
// connect wait never expires.
func TestBinary_AFailedRecoveryAnswersTheHeldRequestAndExitsWithOneErrorLine(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	env := testkit.NewBinaryEnv(t, acdcBinary)
	env.UseBinaryCopy(t)
	stub, spawnedIdentity, err := testkit.WriteRecordingStub(env.Root)
	require.NoError(t, err)
	launch := env.Start(t, sharedLaunchArgs(testkit.CreateTestContentDir(t, nil),
		"--shared-server-linger", "10m", "--shared-server-connect-wait", "10m")...)
	handshake(t, launch, deadline)
	session, logPath := awaitIsolatedAttach(t, env, launch, deadline)
	server := awaitServerRun(t, logPath, "attached:"+session, deadline)

	env.ReplaceBinary(t, stub)
	require.NoError(t, syscall.Kill(server.PID, syscall.SIGKILL))
	_, err = launch.AwaitStderrRecord(slogtext.Record{"level": "WARN", "msg": "Shared server connection lost, recovering"}, deadline)
	require.NoError(t, err)
	require.NoError(t, launch.Send(`{"jsonrpc":"2.0","id":7,"method":"tools/list","params":{}}`))
	id, err := testkit.AwaitFile(spawnedIdentity, deadline)
	require.NoError(t, err)
	endpoints := env.EndpointHarness(t)
	endpoints.RemoveSocket(id)
	endpoints.BreakBeforeReady(id, 0, testkit.CloseConnection)

	answer, err := launch.ReadLine(deadline)
	require.NoError(t, err, "stderr:\n%s", launch.Stderr())
	var line rpcLine
	require.NoError(t, json.Unmarshal([]byte(answer), &line), answer)
	assert.JSONEq(t, `7`, string(line.ID))
	require.NotNil(t, line.Error, answer)
	assert.Equal(t, -32000, line.Error.Code)
	_, err = launch.ReadLine(deadline)
	assert.EqualError(t, err, "stdout ended", "the held request's answer is the only line written")

	code, err := launch.Wait(deadline)
	require.NoError(t, err)
	assert.Equal(t, 1, code)
	stderr := launch.Stderr()
	assert.Len(t, stderrLinesWithPrefix(stderr, "Error: "), 1, stderr)
	assert.Len(t, stderrLinesWithPrefix(stderr, "Error: failed to recover the shared server connection (dropped_twice)"), 1, stderr)
	assert.NotContains(t, stderr, "level=ERROR")
}
