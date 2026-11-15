//go:build unix

package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartDetached_ReturnsWithoutWaitingAndTheChildLeadsItsOwnSession(t *testing.T) {
	deadline := time.Now().Add(spawnChildWatchdog)
	rendezvous := newSpawnRendezvous(t)
	executable, err := os.Executable()
	require.NoError(t, err)

	requireReturnsBeforeRelease(t, deadline, func() error {
		return startDetached(detachedCommand(executable, detachedTestChildArg))
	})

	record := rendezvous.release(t, deadline)
	assert.True(t, record.SessionLeader, "the child is the leader of a session of its own")
}

func TestStartDetached_ReturnsTheStartError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")

	err := startDetached(detachedCommand(missing))

	require.ErrorIs(t, err, fs.ErrNotExist)
}
