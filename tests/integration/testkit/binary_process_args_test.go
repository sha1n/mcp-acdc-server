//go:build linux || darwin

package testkit

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessArgs_ReadsTheArgumentVectorOfALiveProcess(t *testing.T) {
	deadline := time.Now().Add(guardTestBudget)
	args := []string{"--shared-server-identity", fakeIdentity, "an argument with spaces", ""}
	process := startFakeSharedServer(t, deadline, "", args...)

	got, err := processArgs(process.pid)

	require.NoError(t, err)
	assert.Equal(t, append([]string{os.Args[0], "-test.run=^TestFakeSharedServerProcess$", "--"}, args...), got)
}

func TestProcessArgs_FailsForADeadProcess(t *testing.T) {
	_, err := processArgs(startDeadProcess(t, time.Now().Add(guardTestBudget)))

	assert.Error(t, err)
}
