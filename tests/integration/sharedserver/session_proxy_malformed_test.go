//go:build unix

package sharedserver

import (
	"context"
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
	launches := map[string]*testkit.LaunchUnderTest{
		"proxy":       h.Launch(testkit.SharedLaunchArgs(contentDir)...),
		"per-session": testkit.StartLaunch(t, h.SessionProxyParams(sessionArgs), h.Binary, sessionArgs...),
	}

	for name, l := range launches {
		// An answered initialize proves the session is attached before the
		// incomplete line arrives.
		l.WriteLine(rawInitializeRequest)
		assert.Nil(t, decodeRawResponse(t, l.ReadLine()).Error, name)
		l.WriteLine(incomplete)
		l.CloseStdin()

		assert.Empty(t, string(l.ReadToEOF()), name)
		t.Logf("%s ended with: %v", name, l.Wait(testkit.SharedServerDeadline))
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
}
