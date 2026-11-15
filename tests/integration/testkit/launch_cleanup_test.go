//go:build unix

package testkit

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	blockedAnswersChildEnv = "ACDC_TESTKIT_BLOCKED_ANSWERS_CLEANUP"
	blockedAnswersFailure  = "deliberate failure before draining host stdout"
)

// TestLaunchCleanup_BlockedAnswersChild fails by design, so it only runs as
// the child process of
// TestLaunchCleanup_FailedTestEndsALaunchBlockedOnItsAnswers. Its launch has
// decided a failed recovery and is blocked writing the second owed answer to
// host stdout, which host stdin EOF cannot abort.
func TestLaunchCleanup_BlockedAnswersChild(t *testing.T) {
	if os.Getenv(blockedAnswersChildEnv) == "" {
		t.Skip("runs only as the child of the blocked answers cleanup guard")
	}
	h := NewSharedServerHarness(t)
	var l *LaunchUnderTest
	t.Cleanup(func() {
		if l != nil && !l.Ended() {
			t.Error("launch still running after its cleanup")
		}
	})
	var gate *RecoveryGate
	l, gate = h.LaunchGatingRecovery(SharedLaunchArgs(CreateTestContentDir(t, nil),
		"--shared-server-linger", "1h",
		"--shared-server-connect-wait", "20s",
	)...)
	h.Eventually(func() bool { return strings.Contains(l.Stderr(), `msg="Attached to the shared server"`) },
		"the launch attached to its shared server")
	h.StopSharedServers()
	gate.AwaitHeld()
	id := l.SharedServerIdentity()
	h.RemoveSocket(id)
	h.BreakBeforeReady(id, 0, CloseConnection)
	l.WriteLine(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	l.WriteLine(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	// Returns only once the second line was read off the pipe, which is
	// after the first was held.
	l.WriteUnterminated("{")
	gate.Release()
	require.Contains(t, string(l.ReadLine()), `"id":1`, "the failed recovery answers the first held request")

	t.Fatal(blockedAnswersFailure)
}

func TestLaunchCleanup_FailedTestEndsALaunchBlockedOnItsAnswers(t *testing.T) {
	self, err := os.Executable()
	require.NoError(t, err)
	// The timeout only bounds a regression, which would otherwise hang the
	// child in cleanup.
	child := exec.Command(self, "-test.run=^TestLaunchCleanup_BlockedAnswersChild$", "-test.v", "-test.timeout="+(3*SharedServerDeadline).String())
	child.Env = append(os.Environ(), blockedAnswersChildEnv+"=1")

	out, err := child.CombinedOutput()

	var exit *exec.ExitError
	require.True(t, errors.As(err, &exit), "the child must fail by design: %v\n%s", err, out)
	assert.Contains(t, string(out), blockedAnswersFailure, "the child reached its blocked answers:\n%s", out)
	assert.Contains(t, string(out), "--- FAIL: TestLaunchCleanup_BlockedAnswersChild")
	assert.NotContains(t, string(out), "test timed out")
	assert.NotContains(t, string(out), "launch did not return")
	assert.NotContains(t, string(out), "launch still running")
}
