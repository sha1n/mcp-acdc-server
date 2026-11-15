//go:build unix

package sharedserver

import (
	"strings"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
)

// The connect wait bounds the first connect too, so the launch attaches to a
// shared server that is already listening and its first dial succeeds at
// once. The recovery's connect wait starts only when the gate releases it,
// after the test holds the lock and every outage line is held, so it can only
// expire.
func TestSessionFailedRecovery_ConnectWaitExpiryAnswersEveryHeldRequestInHeldOrder(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	args := recoveryLaunchArgs(newSessionProxyContentDir(t), "--shared-server-connect-wait", "2s")
	h := testkit.NewSharedServerHarness(t)
	id, _ := h.HoldSharedServer(args)
	l, gate := h.LaunchGatingRecovery(args...)
	attachRecords(t, l, 1, deadline)

	crashIntoHeldRecovery(t, h, l, gate, deadline)
	h.HoldLock(id)
	outage := []string{
		modernSearch("7", "outage-first"),
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":7,"reason":"host gave up"}}`,
		modernSearch(`"s-1"`, "outage-second"),
		`{"jsonrpc":"2.0","id":8,"result":{}}`,
		modernSearch("7", "outage-duplicate"),
		`not a json-rpc message`,
	}
	for _, line := range outage {
		l.WriteLine(line)
	}
	l.WriteUnterminated("{")
	gate.Release()

	want := strings.Join([]string{connectionLostError("7"), connectionLostError(`"s-1"`), connectionLostError("7")}, "")
	assert.Equal(t, want, string(l.ReadToEOF()),
		"every held request gets the connection-lost error in held order, and nothing else is written")
	requireFailedRecovery(t, h, l, "connect_wait")
	assert.Equal(t, 1, h.Builds(), "no shared server was built while the test held the lock")
}
