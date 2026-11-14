//go:build unix

package sharedserver

import (
	"os"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionProxy_SessionModeRunsAPerSessionServerWithoutTouchingSharedState(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	l := h.Launch(testkit.SharedLaunchArgs(newSessionProxyContentDir(t), "--server-mode", "session")...)

	session := connectAndListTools(t, l)
	require.NoError(t, session.Close())
	require.NoError(t, l.Wait(testkit.SharedServerDeadline))

	assert.Equal(t, 0, h.Spawns())
	assert.False(t, h.EndpointDirExists())
	entries, err := os.ReadDir(h.LogDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
	stderr := l.Stderr()
	assert.NotEmpty(t, stderr)
	for _, line := range strings.Split(stderr, "\n") {
		assert.NotContains(t, line, " session=")
	}
}
