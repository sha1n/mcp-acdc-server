//go:build unix

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A zombie still answers kill 0, so ESRCH proves the child was reaped. The
// pid comes from the child itself because Release discards cmd.Process.Pid.
func TestStartDetached_ReapsExitedChild(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")

	require.NoError(t, startDetached(detachedCommand("/bin/sh", "-c", `echo $$ > "$0.tmp" && mv "$0.tmp" "$0"; exit 0`, pidFile)))

	var pid int
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
		return err == nil
	}, 5*time.Second, 10*time.Millisecond, "the child never recorded its pid")

	require.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	}, 5*time.Second, 10*time.Millisecond, "child %d exited but was never reaped: it remains a zombie of the launching process", pid)
}
