//go:build unix

package sharedserver

import (
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
)

const rawLegacySearch = `{"jsonrpc":"2.0","id":"after","method":"tools/call","params":{"name":"search","arguments":{"query":"authentication"}}}`

// The replacement records the replayed setLevel before answering it, and the
// proxy waits for that answer before it sends the search, so the record is
// in place, and every line the proxy wrote to the host before the search
// response has been read, once that response is read.
func TestSessionLegacyReplay_PriorSetLevelIsReplayedAndItsResponseDiscarded(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	h := testkit.NewSharedServerHarness(t)
	received := h.RecordReceived()
	l := h.Launch(recoveryLaunchArgs(newSessionProxyContentDir(t))...)
	initializeRawLegacy(t, l)
	l.WriteLine(`{"jsonrpc":"2.0","id":"level","method":"logging/setLevel","params":{"level":"debug"}}`)
	requireResponse(t, l, `"level"`)

	h.StopSharedServers()
	awaitStderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage}, 1, deadline)
	l.WriteLine(rawLegacySearch)
	beforeSearch := readUntilResponse(t, l, `"after"`)

	assert.Equal(t, 2, countReceived(received.Received(), "logging/setLevel"),
		"the replacement shared server received the replayed setLevel")
	assert.Empty(t, responsesTo(t, beforeSearch, `"level"`),
		"the host saw only the response of the first shared server to its setLevel")
	assert.Equal(t, 2, h.Builds(), "the search was answered by a new shared server")
}
