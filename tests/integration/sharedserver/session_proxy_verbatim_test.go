//go:build unix

package sharedserver

import (
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rawLineSession interface {
	WriteLine(line string)
	ReadLine() []byte
}

// exchangeSearchSession waits for each response before the next request so
// the response order, and therefore the byte stream, is deterministic.
func exchangeSearchSession(s rawLineSession) [][]byte {
	s.WriteLine(rawInitializeRequest)
	initialize := s.ReadLine()
	s.WriteLine(rawInitializedNotice)
	s.WriteLine(rawToolsListRequestID2)
	toolsList := s.ReadLine()
	s.WriteLine(rawSearchCallRequestID3)
	return [][]byte{initialize, toolsList, s.ReadLine()}
}

func TestSessionProxy_RelaysSharedServerBytesVerbatim(t *testing.T) {
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t))
	var viaProxy, direct [][]byte

	h := runWinningColdStart(t, func(t *testing.T, h *testkit.SharedServerHarness) {
		viaProxy = exchangeSearchSession(h.Launch(args...))
		direct = exchangeSearchSession(h.DialSharedServer(h.Identity(args)))
	})

	require.Len(t, viaProxy, len(direct))
	for i := range direct {
		assert.Equal(t, direct[i], viaProxy[i], "line %d", i)
	}
	assert.Contains(t, string(viaProxy[2]), "Authentication Guide")
	assert.Equal(t, 1, h.Builds(), "both sessions are served by one shared server")
}
