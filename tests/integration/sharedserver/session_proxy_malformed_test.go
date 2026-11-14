//go:build unix

package sharedserver

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionProxy_IncompleteLineAtStdinEOFEndsTheSessionWithoutOutputAsAPerSessionServer(t *testing.T) {
	const incomplete = `{"jsonrpc":"2.0","id":1,`
	h := testkit.NewSharedServerHarness(t)
	contentDir := newSessionProxyContentDir(t)
	sessionArgs := testkit.SharedLaunchArgs(contentDir, "--server-mode", "session")
	launches := map[string]struct {
		launch     *testkit.LaunchUnderTest
		assertExit func(t *testing.T, err error)
	}{
		"proxy": {
			launch:     h.Launch(testkit.SharedLaunchArgs(contentDir)...),
			assertExit: func(t *testing.T, err error) { assert.NoError(t, err, "the session proxy ends normally at stdin EOF") },
		},
		"per-session": {
			launch:     testkit.StartLaunch(t, h.SessionProxyParams(sessionArgs), h.Binary, sessionArgs...),
			assertExit: func(t *testing.T, err error) { assert.ErrorIs(t, err, io.ErrUnexpectedEOF) },
		},
	}

	for name, launch := range launches {
		l := launch.launch
		// An answered initialize proves the session is attached before the
		// incomplete line arrives.
		l.WriteLine(rawInitializeRequest)
		assert.Nil(t, decodeRawResponse(t, l.ReadLine()).Error, name)
		l.WriteLine(incomplete)
		l.CloseStdin()

		assert.Empty(t, string(l.ReadToEOF()), name)
		launch.assertExit(t, l.Wait(testkit.SharedServerDeadline))
	}
}

func TestSessionProxy_InvalidLineEndsOnlyItsSessionAndIsLoggedByTheSharedServer(t *testing.T) {
	const invalid = `{"jsonrpc":"2.0","id":1,}`
	h := testkit.NewSharedServerHarness(t)
	contentDir := newSessionProxyContentDir(t)
	sharedArgs := testkit.SharedLaunchArgs(contentDir)
	sessionArgs := testkit.SharedLaunchArgs(contentDir, "--server-mode", "session")

	perSession := testkit.StartLaunch(t, h.SessionProxyParams(sessionArgs), h.Binary, sessionArgs...)
	perSession.WriteLine(invalid)
	perSessionErr := perSession.Wait(testkit.SharedServerDeadline)
	require.Error(t, perSessionErr)

	first := connectAndListTools(t, h.Launch(sharedArgs...))
	logPath := h.DialSharedServer(h.Identity(sharedArgs)).Ready.Log
	second := h.Launch(sharedArgs...)
	second.WriteLine(invalid)

	require.Error(t, second.Wait(testkit.SharedServerDeadline))
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
