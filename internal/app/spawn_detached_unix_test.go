//go:build unix

package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// detachedChildBudget is generous: it bounds a hang, it does not measure the
// start.
const detachedChildBudget = 30 * time.Second

// detachedChildScript publishes its pid atomically and then runs until the
// test removes the file named by its second argument, so the child ends even
// when the test never learns its pid.
const detachedChildScript = `echo $$ > "$1.tmp" && mv "$1.tmp" "$1"; while [ -e "$2" ]; do sleep 0.05; done`

func TestStartDetached_ReturnsWithoutWaitingAndTheChildLeadsItsOwnSession(t *testing.T) {
	deadline := time.Now().Add(detachedChildBudget)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	alive := filepath.Join(dir, "alive")
	require.NoError(t, os.WriteFile(alive, nil, 0o600))
	t.Cleanup(func() { endDetachedChild(t, alive, pidFile) })

	started := make(chan error, 1)
	go func() {
		started <- startDetached(detachedCommand("/bin/sh", "-c", detachedChildScript, "sh", pidFile, alive))
	}()
	select {
	case err := <-started:
		require.NoError(t, err)
	case <-time.After(time.Until(deadline)):
		require.FailNow(t, "startDetached waited for a child that runs until the test ends it")
	}

	pid := awaitDetachedChildPID(t, pidFile, deadline)
	sid, err := unix.Getsid(pid)
	require.NoError(t, err)
	assert.Equal(t, pid, sid, "the child is the leader of a session of its own")
}

func TestStartDetached_ReturnsTheStartError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")

	err := startDetached(detachedCommand(missing))

	require.ErrorIs(t, err, fs.ErrNotExist)
}

func awaitDetachedChildPID(t *testing.T, pidFile string, deadline time.Time) int {
	t.Helper()
	for {
		pid, ok, err := readDetachedChildPID(pidFile)
		require.NoError(t, err)
		if ok {
			return pid
		}
		require.True(t, time.Now().Before(deadline), "the detached child did not publish its pid")
		time.Sleep(10 * time.Millisecond)
	}
}

func readDetachedChildPID(pidFile string) (int, bool, error) {
	data, err := os.ReadFile(pidFile)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid, err == nil, err
}

// endDetachedChild removes the liveness file first, so a child that has not
// published its pid yet ends by itself; a known child is killed and reaped,
// since a released process is still a child of the test process.
func endDetachedChild(t *testing.T, alive, pidFile string) {
	if err := os.Remove(alive); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("failed to remove the liveness file: %v", err)
	}
	pid, ok, err := readDetachedChildPID(pidFile)
	if err != nil {
		t.Errorf("failed to read the child pid: %v", err)
	}
	if !ok {
		return
	}
	if err := unix.Kill(pid, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
		t.Errorf("failed to kill the detached child %d: %v", pid, err)
	}
	if _, err := unix.Wait4(pid, nil, 0, nil); err != nil {
		t.Errorf("failed to reap the detached child %d: %v", pid, err)
	}
}
