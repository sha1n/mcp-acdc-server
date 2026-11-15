//go:build unix

package sharedserver

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func modernDiscover(id string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"server/discover","params":{%s}}`, id, modernMeta)
}

func discoveredInstructions(t *testing.T, line []byte, id string) string {
	t.Helper()
	resp := decodeRawResponse(t, line)
	require.JSONEq(t, id, string(resp.ID))
	require.Nil(t, resp.Error, "server/discover fails: %s", line)
	var result struct {
		Instructions string `json:"instructions"`
	}
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	return result.Instructions
}

// The server instructions come from the manifest, which a shared server reads
// only at startup: a running shared server keeps serving the old value, so
// only a recovery that recomputes the identity can show the new one.
func TestSessionRecovery_RecoveryAfterAManifestEditCompletesOnASharedServerForTheNewIdentity(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	args := recoveryLaunchArgs(newStartupFixedContentDir(t))
	h := testkit.NewSharedServerHarness(t)
	idBefore := h.Identity(args)
	l := h.Launch(args...)
	first := attachRecords(t, l, 1, deadline)[0]
	l.WriteLine(modernDiscover("1"))
	assert.Equal(t, originalInstructions, discoveredInstructions(t, l.ReadLine(), "1"))

	editManifest(t, args[1])
	idAfter := h.Identity(args)
	require.NotEqual(t, idBefore, idAfter, "the manifest edit must change the server identity")
	l.WriteLine(modernDiscover("2"))
	assert.Equal(t, originalInstructions, discoveredInstructions(t, l.ReadLine(), "2"), "the running shared server keeps the manifest it started with")
	h.StopSharedServers()

	warnings := awaitStderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage}, 1, deadline)
	assert.Equal(t, "0", warnings[0]["in_flight"])
	l.WriteLine(modernDiscover("3"))
	assert.Equal(t, editedInstructions, discoveredInstructions(t, l.ReadLine(), "3"), "the recovered session is served by a shared server started from the edited manifest")

	attaches := attachRecords(t, l, 2, deadline)
	require.Len(t, attaches, 2)
	second := attaches[1]
	assert.Equal(t, filepath.Join(h.LogDir, idBefore+".log"), first["log"])
	assert.Equal(t, filepath.Join(h.LogDir, idAfter+".log"), second["log"], "the recovery attached to the shared server of the new identity")
	assert.NotEqual(t, first["session"], second["session"])
	socketPath, err := h.Endpoint().SocketPath(idAfter)
	require.NoError(t, err)
	assert.FileExists(t, socketPath, "the shared server of the new identity is running")
	assert.Equal(t, 2, h.Builds(), "the recovery built a shared server for the new identity")
	starts := serverLogRecords(t, second["log"], slogtext.Record{"msg": serverStartupMessage})
	assert.Len(t, starts, 1, "one shared server started for the new identity")
	attached := awaitServerLogRecords(t, second["log"], slogtext.Record{"msg": sessionAttachedMessage}, 1, deadline)
	assert.Equal(t, []string{second["session"]}, sessionsOf(attached))
	awaitStderrRecords(t, l, slogtext.Record{"level": "INFO", "msg": recoveredMessage}, 1, deadline)
	requireNoProxyRecordsInServerLog(t, second["log"])
	assert.Zero(t, h.FallbackWarnings())
}

// The fake old shared server answers the request with a complete line and
// hangs up mid-line; the same fake serves the recovered connection, so every
// byte the host can receive is fixed by the test.
func TestSessionRecovery_IncompleteTrailingLineFromTheOldServerIsNotDeliveredToTheHost(t *testing.T) {
	const (
		firstAnswer  = `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"first"}]}}` + "\n"
		partialLine  = `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"partial`
		secondAnswer = `{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"second"}]}}` + "\n"
	)
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	args := recoveryLaunchArgs(newSessionProxyContentDir(t))
	h := testkit.NewSharedServerHarness(t)
	received := h.ServeScripted(h.Identity(args),
		testkit.ScriptedConnection{Reply: firstAnswer + partialLine, HangUp: true},
		testkit.ScriptedConnection{Reply: secondAnswer},
	)
	l := h.Launch(args...)
	attachRecords(t, l, 1, deadline)

	l.WriteLine(modernSearch("1", "authentication"))
	assert.Equal(t, firstAnswer, string(l.ReadLine()), "the complete line before the hang-up reaches the host")
	attachRecords(t, l, 2, deadline)
	l.WriteLine(modernSearch("2", "authentication"))
	assert.Equal(t, secondAnswer, string(l.ReadLine()), "the next host line starts cleanly, without the incomplete bytes")
	l.CloseStdin()
	assert.Empty(t, string(l.ReadToEOF()), "nothing else reaches the host")
	require.NoError(t, l.Wait(testkit.SharedServerDeadline))

	assert.Equal(t, [][]string{
		{modernSearch("1", "authentication") + "\n"},
		{modernSearch("2", "authentication") + "\n"},
	}, received(), "each connection received exactly its request")
	warnings := stderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage})
	require.Len(t, warnings, 1)
	assert.Equal(t, "0", warnings[0]["in_flight"], "the complete line answered the only request")
	assert.Len(t, stderrRecords(t, l, slogtext.Record{"level": "INFO", "msg": recoveredMessage}), 1)
	assert.Zero(t, h.Spawns(), "the recovery dialed the fake again")
	assert.Zero(t, h.FallbackWarnings())
}
