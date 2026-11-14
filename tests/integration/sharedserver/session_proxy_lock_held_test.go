//go:build unix

package sharedserver

import (
	"os"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionProxy_KeepsSpawningWhileTheLockIsHeldAndAttachesOnceItIsReleased(t *testing.T) {
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t), "--shared-server-connect-wait", "30s")

	h := runWinningColdStart(t, func(t *testing.T, h *testkit.SharedServerHarness) {
		id := h.Identity(args)
		release := h.HoldLock(id)
		l := h.Launch(args...)

		h.Eventually(func() bool { return h.Spawns() >= 2 }, "the proxy keeps spawning candidates while the lock is held")

		assert.Equal(t, 0, h.Builds(), "no candidate builds while the lock is held")
		socketPath, err := h.Endpoint().SocketPath(id)
		require.NoError(t, err)
		_, err = os.Lstat(socketPath)
		assert.True(t, os.IsNotExist(err), "no socket is bound while the lock is held: %v", err)

		release()
		connectAndListTools(t, l)
	})

	assert.Equal(t, 1, h.Builds())
}
