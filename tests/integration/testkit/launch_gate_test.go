//go:build unix

package testkit

import (
	"errors"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const gateEarlyFailureEnv = "ACDC_TESTKIT_GATE_EARLY_FAILURE"

// TestLaunchGate_EarlyFailureChild fails by design, so it only runs as the
// child process of TestLaunchGate_LaunchFailingBeforeItStartsDoesNotBlockCleanup.
func TestLaunchGate_EarlyFailureChild(t *testing.T) {
	if os.Getenv(gateEarlyFailureEnv) == "" {
		t.Skip("runs only as the child of the early failure guard")
	}
	h := NewSharedServerHarness(t)
	gate := NewLaunchGate()
	h.LaunchBehind(gate, "--content-dir", t.TempDir())
	h.LaunchBehind(gate, "--invalid-gate-flag")
}

func TestLaunchGate_LaunchFailingBeforeItStartsDoesNotBlockCleanup(t *testing.T) {
	self, err := os.Executable()
	require.NoError(t, err)
	// The timeout only bounds a regression, which would otherwise hang the
	// child in cleanup forever; a passing child ends as soon as it fails.
	child := exec.Command(self, "-test.run=^TestLaunchGate_EarlyFailureChild$", "-test.v", "-test.timeout="+(3*SharedServerDeadline).String())
	child.Env = append(os.Environ(), gateEarlyFailureEnv+"=1")

	out, err := child.CombinedOutput()

	var exit *exec.ExitError
	require.True(t, errors.As(err, &exit), "the child must fail on its invalid flag: %v\n%s", err, out)
	assert.Contains(t, string(out), "unknown flag: --invalid-gate-flag")
	assert.Contains(t, string(out), "--- FAIL: TestLaunchGate_EarlyFailureChild")
	assert.NotContains(t, string(out), "test timed out")
	assert.NotContains(t, string(out), "launch did not return")
}

func TestLaunchGate_ReleaseIsIdempotent(t *testing.T) {
	gate := NewLaunchGate()

	gate.Release()
	gate.Release()

	var unset *LaunchGate
	unset.Release()
	select {
	case <-gate.open:
	default:
		t.Fatal("gate is not open after Release")
	}
}
