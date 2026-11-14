//go:build unix

package sharedserver

import (
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionProxy_RelaysInputHeldBeforeTheReadyLineInOrderAndNeverForwardsTheReadyLine(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t), "--shared-server-connect-wait", "1m")
	release := h.HoldLock(h.Identity(args))
	l := h.Launch(args...)
	h.Eventually(func() bool { return h.Spawns() >= 1 }, "the proxy is discovering")

	l.WriteLine(rawInitializeRequest)
	l.WriteLine(rawInitializedNotice)
	l.WriteLine(rawToolsListRequestID2)
	release()
	l.WriteLine(rawToolsListRequestID3)

	lines := [][]byte{l.ReadLine(), l.ReadLine(), l.ReadLine()}

	for _, line := range lines {
		_, err := sharedserver.DecodeReadyLine(line)
		require.Error(t, err, "the ready line reached the host: %s", line)
	}
	ids := make([]string, 0, len(lines))
	for _, line := range lines {
		resp := decodeRawResponse(t, line)
		assert.Nil(t, resp.Error, "line: %s", line)
		assert.NotEmpty(t, resp.Result, "line: %s", line)
		ids = append(ids, string(resp.ID))
	}
	assert.Equal(t, "1", ids[0], "the held initialize is answered first")
	assert.ElementsMatch(t, []string{"2", "3"}, ids[1:])
}
