//go:build unix

package sharedserver

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionProxy_IncompleteLineAtStdinEOFEndsTheSessionWithoutOutputAsAPerSessionServer(t *testing.T) {
	const incomplete = `{"jsonrpc":"2.0","id":1,`
	contentDir := newSessionProxyContentDir(t)
	proxyArgs := testkit.SharedLaunchArgs(contentDir)
	sessionArgs := testkit.SharedLaunchArgs(contentDir, "--server-mode", "session")
	// An answered initialize proves the session is attached before the
	// incomplete line arrives.
	attach := func(t *testing.T, l *testkit.LaunchUnderTest, name string) *testkit.LaunchUnderTest {
		l.WriteLine(rawInitializeRequest)
		assert.Nil(t, decodeRawResponse(t, l.ReadLine()).Error, name)
		return l
	}
	var proxy *testkit.LaunchUnderTest
	runWinningColdStart(t, func(t *testing.T, h *testkit.SharedServerHarness) {
		proxy = attach(t, h.Launch(proxyArgs...), "proxy")
	})
	reference := testkit.NewSharedServerHarness(t)
	perSession := attach(t, testkit.StartLaunch(t, reference.SessionProxyParams(sessionArgs), reference.Binary, sessionArgs...), "per-session")
	launches := map[string]struct {
		launch     *testkit.LaunchUnderTest
		assertExit func(t *testing.T, err error)
	}{
		"proxy": {
			launch:     proxy,
			assertExit: func(t *testing.T, err error) { assert.NoError(t, err, "the session proxy ends normally at stdin EOF") },
		},
		"per-session": {
			launch:     perSession,
			assertExit: func(t *testing.T, err error) { assert.ErrorIs(t, err, io.ErrUnexpectedEOF) },
		},
	}

	for name, launch := range launches {
		l := launch.launch
		l.WriteLine(incomplete)
		l.CloseStdin()

		assert.Empty(t, string(l.ReadToEOF()), name)
		launch.assertExit(t, l.Wait(testkit.SharedServerDeadline))
	}
}

func TestSessionProxy_InvalidLineEndsOnlyItsSharedServerSessionWhichRecoversAndIsLoggedByTheSharedServer(t *testing.T) {
	const invalid = `{"jsonrpc":"2.0","id":1,}`
	contentDir := newSessionProxyContentDir(t)
	sharedArgs := testkit.SharedLaunchArgs(contentDir)
	sessionArgs := testkit.SharedLaunchArgs(contentDir, "--server-mode", "session")

	reference := testkit.NewSharedServerHarness(t)
	perSession := testkit.StartLaunch(t, reference.SessionProxyParams(sessionArgs), reference.Binary, sessionArgs...)
	perSession.WriteLine(invalid)
	perSessionErr := perSession.Wait(testkit.SharedServerDeadline)
	require.Error(t, perSessionErr)

	var first *mcp.ClientSession
	h := runWinningColdStart(t, func(t *testing.T, h *testkit.SharedServerHarness) {
		first = connectAndListTools(t, h.Launch(sharedArgs...))
	})
	logPath := h.DialSharedServer(h.Identity(sharedArgs)).Ready.Log
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	second := h.Launch(sharedArgs...)
	before := attachRecords(t, second, 1, deadline)[0]
	second.WriteLine(invalid)

	after := attachRecords(t, second, 2, deadline)[1]
	assert.NotEqual(t, before["session"], after["session"], "the session recovers with a new session identifier")
	assert.Equal(t, before["log"], after["log"], "the session recovers onto the same shared server")
	second.WriteLine(modernSearch("3", "authentication"))
	resp := decodeRawResponse(t, second.ReadLine())
	assert.JSONEq(t, "3", string(resp.ID), "the invalid line gets no synthesized error")
	assert.Nil(t, resp.Error, "a later request succeeds")
	assert.Len(t, stderrRecords(t, second, slogtext.Record{"msg": crashWarningMessage}), 1, "the invalid line is not delivered again")
	assert.Equal(t, 1, h.Builds(), "the session recovers onto the same shared server")
	h.Eventually(func() bool {
		content, err := os.ReadFile(logPath)
		return err == nil && strings.Contains(string(content), perSessionErr.Error())
	}, "the shared server logs the session error a per-session server returns")
	ctx, cancel := context.WithTimeout(context.Background(), testkit.SharedServerDeadline)
	defer cancel()
	_, err := first.ListTools(ctx, nil)
	require.NoError(t, err, "the shared server keeps serving other sessions")
	assert.Zero(t, h.FallbackWarnings(), "both sessions were attached to the shared server")
}
