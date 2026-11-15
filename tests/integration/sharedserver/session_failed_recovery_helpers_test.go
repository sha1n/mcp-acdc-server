//go:build unix

package sharedserver

import (
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	failedRecoveryErrorPrefix = "failed to recover the shared server connection"
	perSessionStartupMessage  = "Registered tool"
)

// crashIntoHeldRecovery stops every shared server and returns once the crash
// was observed, the old shared server has exited and the recovery is held at
// its endpoint step: host input is held and no connect wait runs.
func crashIntoHeldRecovery(t *testing.T, h *testkit.SharedServerHarness, l *testkit.LaunchUnderTest, gate *testkit.RecoveryGate, deadline time.Time) {
	t.Helper()
	h.StopSharedServers()
	awaitStderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage}, 1, deadline)
	gate.AwaitHeld()
}

// requireFailedRecovery asserts the runner's result and its stderr once the
// launch has returned: the error names the failed recovery and its cause,
// the proxy wrote no line of its own for the failure, and the session was
// not handed to a per-session server.
func requireFailedRecovery(t *testing.T, h *testkit.SharedServerHarness, l *testkit.LaunchUnderTest, cause string) {
	t.Helper()
	err := l.Wait(testkit.SharedServerDeadline)
	require.Error(t, err, "a failed recovery ends the runner with an error")
	assert.Contains(t, err.Error(), failedRecoveryErrorPrefix+" ("+cause+")")

	records := stderrRecords(t, l, slogtext.Record{})
	for _, record := range records {
		assert.NotEqual(t, "ERROR", record["level"], "the proxy writes no error line for the failure: %v", record)
		assert.NotContains(t, record["msg"], failedRecoveryErrorPrefix)
		assert.NotEqual(t, perSessionStartupMessage, record["msg"], "no per-session server was built")
	}
	assert.Empty(t, l.FallbackWarnings(), "a failed recovery never falls back: %s", l.Stderr())
	assert.Zero(t, h.FallbackWarnings())
	assert.Equal(t, []string{attachMessage, crashWarningMessage}, recoveryLifecycleOf(t, l),
		"stderr has the attach line and the one crash warning only")
}
