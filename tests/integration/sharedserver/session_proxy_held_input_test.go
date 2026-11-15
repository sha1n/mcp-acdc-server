//go:build unix

package sharedserver

import (
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionProxy_AnswersInputHeldBeforeTheReadyLineThroughTheSharedServer(t *testing.T) {
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t), "--shared-server-connect-wait", "1m")
	var lines [][]byte

	h := runWinningColdStart(t, func(t *testing.T, h *testkit.SharedServerHarness) {
		release := h.HoldLock(h.Identity(args))
		l := h.Launch(args...)
		h.Eventually(func() bool { return h.Spawns() >= 1 }, "the proxy is discovering")

		l.WriteLine(rawInitializeRequest)
		l.WriteLine(rawInitializedNotice)
		l.WriteLine(rawToolsListRequestID2)
		release()
		l.WriteLine(rawToolsListRequestID3)

		lines = [][]byte{l.ReadLine(), l.ReadLine(), l.ReadLine()}
	})

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
	assert.Zero(t, h.FallbackWarnings(), "the held input was answered by the shared server")
}

// The shared server answers calls concurrently, so arrival order is observed
// on a socket that records it while the test holds the lock.
func TestSessionProxy_RelaysInputHeldBeforeTheReadyLineInOrderAndNeverForwardsTheReadyLine(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t), "--shared-server-connect-wait", "1m")
	id := h.Identity(args)
	h.HoldLock(id)
	l := h.Launch(args...)
	h.Eventually(func() bool { return h.Spawns() >= 1 }, "the proxy is discovering")
	held := []string{rawInitializeRequest, rawInitializedNotice, rawToolsListRequestID2}
	for _, line := range held {
		l.WriteLine(line)
	}

	received := h.ServeReadyWithoutAnswering(id)
	l.WriteLine(rawToolsListRequestID3)

	want := make([]string, 0, len(held)+1)
	for _, line := range append(held, rawToolsListRequestID3) {
		want = append(want, line+"\n")
	}
	h.Eventually(func() bool { return len(received()) >= len(want) }, "every line reaches the shared server")
	assert.Equal(t, want, received(), "held lines arrive in order, before the live one")
	l.CloseStdin()
	assert.Empty(t, string(l.ReadToEOF()), "the ready line never reaches the host")
}
