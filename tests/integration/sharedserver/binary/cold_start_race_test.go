//go:build unix

package binary

import (
	"fmt"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// coldStartRaceRounds repeats the race in fresh environments: a single
// round may be won before the second session dials, a repetition makes a
// shared lock contest likely in at least one of them.
const coldStartRaceRounds = 5

// Both sessions are held behind one barrier before exec and released by one
// close, so they start discovery against the cold identity together. A
// candidate that loses the lock exits before opening the log, so the log
// counts runs, not spawns; the long linger means no run can expire and be
// replaced, and both sessions are attached when the log is read.
func TestBinary_TwoSessionsRacingAColdStartAttachToOneSharedServer(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	for round := range coldStartRaceRounds {
		t.Run(fmt.Sprintf("round %d", round+1), func(t *testing.T) {
			raceColdStart(t, deadline)
		})
	}
}

func raceColdStart(t *testing.T, deadline time.Time) {
	env := testkit.NewBinaryEnv(t, acdcBinary)
	args := sharedLaunchArgs(testkit.CreateTestContentDir(t, nil), "--shared-server-linger", longLinger)
	barrier := testkit.NewStartBarrier(t)
	first := env.StartBehind(t, barrier, args...)
	second := env.StartBehind(t, barrier, args...)
	require.NoError(t, barrier.Release())

	handshake(t, first, deadline)
	handshake(t, second, deadline)
	firstSession, firstLog := awaitIsolatedAttach(t, env, first, deadline)
	secondSession, secondLog := awaitIsolatedAttach(t, env, second, deadline)
	require.Equal(t, firstLog, secondLog)
	served := awaitServerRun(t, firstLog, "attached:"+firstSession, deadline)
	awaitServerRun(t, firstLog, "attached:"+secondSession, deadline)

	runs, err := testkit.ReadServerRuns(firstLog)
	require.NoError(t, err)
	require.Len(t, runs, 1, "one startup entry")
	assert.Equal(t, served.PID, runs[0].PID)
	assert.Contains(t, runs[0].Sessions, "attached:"+firstSession)
	assert.Contains(t, runs[0].Sessions, "attached:"+secondSession)
	assert.True(t, runs[0].Stopped.IsZero())
	endSession(t, first, deadline)
	endSession(t, second, deadline)
}
