//go:build unix

package binary

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/stretchr/testify/require"
)

// The session ends before the kill, so no proxy can spawn a server that
// retakes the lock: only the kernel can release it.
func TestBinary_AfterASIGKILLTheSharedServersLockIsAcquirable(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	s := startAttachedSession(t, deadline)
	endSession(t, s.launch, deadline)
	lingering := awaitServerRun(t, s.logPath, "detached:"+s.session, deadline)

	require.NoError(t, s.env.SignalSharedServer(s.logPath, lingering, syscall.SIGKILL))

	ep, err := endpoint.OpenEndpoint(s.env.EndpointDir())
	require.NoError(t, err)
	lockPath, err := ep.LockPath(strings.TrimSuffix(filepath.Base(s.logPath), ".log"))
	require.NoError(t, err)
	lock := flock.New(lockPath)
	for {
		locked, err := lock.TryLock()
		require.NoError(t, err)
		if locked {
			break
		}
		require.True(t, time.Now().Before(deadline), "%s was not released after the kill", lockPath)
		time.Sleep(20 * time.Millisecond)
	}
	require.NoError(t, lock.Unlock())
}
