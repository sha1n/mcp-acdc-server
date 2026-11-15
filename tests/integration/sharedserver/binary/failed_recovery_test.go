//go:build unix

package binary

import (
	"encoding/json"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const failedRecoveryErrorLine = "Error: failed to recover the shared server connection (dropped_twice)"

func TestBinary_AFailedRecoveryAnswersTheHeldRequestAndExitsWithOneErrorLine(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)

	launch, _ := failRecoveryWithAHeldRequest(t, deadline, (*testkit.BinaryEnv).Start)

	stderr := launch.Stderr()
	assert.Len(t, stderrLinesWithPrefix(stderr, "Error: "), 1, stderr)
	assert.Len(t, stderrLinesWithPrefix(stderr, failedRecoveryErrorLine), 1, stderr)
	assert.NotContains(t, stderr, "level=ERROR")
}

// One pipe carries both streams here, so the order of the lines in it is the
// order of the writes; the test above attributes them to their streams.
func TestBinary_AFailedRecoveryAnswersTheHeldRequestBeforeWritingItsErrorLine(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)

	launch, answer := failRecoveryWithAHeldRequest(t, deadline, (*testkit.BinaryEnv).StartInterleaved)

	output := strings.Split(launch.Output(), "\n")
	answerAt, errorAt := -1, -1
	for i, line := range output {
		switch {
		case line == answer:
			answerAt = i
		case strings.HasPrefix(line, "Error: "):
			require.Equal(t, -1, errorAt, "more than one error line:\n%s", launch.Output())
			errorAt = i
		}
	}
	require.NotEqual(t, -1, answerAt, "the answer is not in the output:\n%s", launch.Output())
	require.NotEqual(t, -1, errorAt, "no error line in the output:\n%s", launch.Output())
	assert.True(t, strings.HasPrefix(output[errorAt], failedRecoveryErrorLine), output[errorAt])
	assert.Less(t, answerAt, errorAt, "the held request is answered before the error line is written:\n%s", launch.Output())
}

// failRecoveryWithAHeldRequest replaces the binary by a stub before the
// server is killed, so every respawn of the recovery yields no server. The
// identity hashes the executable, so the recovery dials the stub's identity,
// which the stub records; the test then ends the recovery itself by serving
// that socket path with a listener that drops every connection before the
// ready line. The connect wait never expires. It returns the exited launch
// and the held request's answer, its only stdout line after the handshake.
func failRecoveryWithAHeldRequest(t *testing.T, deadline time.Time, start func(*testkit.BinaryEnv, testing.TB, ...string) *testkit.BinaryLaunch) (*testkit.BinaryLaunch, string) {
	t.Helper()
	env := testkit.NewBinaryEnv(t, acdcBinary)
	env.UseBinaryCopy(t)
	stub, spawnedIdentity, err := testkit.WriteRecordingStub(env.Root)
	require.NoError(t, err)
	launch := start(env, t, sharedLaunchArgs(testkit.CreateTestContentDir(t, nil),
		"--shared-server-linger", "10m")...)
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
	return launch, answer
}
