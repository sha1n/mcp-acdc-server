//go:build unix

package sharedserver

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const malformedManifest = "server: [unclosed\n"

func TestSessionProxyFallback_PersistentlyUnreadableManifestFailsAsAPerSessionLaunch(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	contentDir := newSessionProxyContentDir(t)
	manifest := filepath.Join(contentDir, ".acdc", "config.yaml")
	require.NoError(t, os.Chmod(manifest, 0o000))
	t.Cleanup(func() {
		if err := os.Chmod(manifest, 0o644); err != nil {
			t.Errorf("failed to restore the manifest mode: %v", err)
		}
	})
	args := testkit.SharedLaunchArgs(contentDir)
	sessionArgs := testkit.SharedLaunchArgs(contentDir, "--server-mode", "session")

	l, gate := testkit.StartHeldLaunch(t, h.SessionProxyParams(args), h.Binary, args...)
	proxyErr := l.Wait(testkit.SharedServerDeadline)
	reference := testkit.StartLaunch(t, h.SessionProxyParams(sessionArgs), h.Binary, sessionArgs...)
	referenceErr := reference.Wait(testkit.SharedServerDeadline)

	require.Error(t, proxyErr)
	require.Error(t, referenceErr)
	assert.Equal(t, referenceErr.Error(), proxyErr.Error())
	assert.Zero(t, gate.Resolutions(), "discovery stops before the endpoint step")
	assert.False(t, h.EndpointDirExists())
	assert.Zero(t, h.Spawns())
	warnings := l.FallbackWarnings()
	require.Len(t, warnings, 1, "stderr: %s", l.Stderr())
	assert.Contains(t, warnings[0], "cause=identity")
	assert.Empty(t, string(l.ReadToEOF()))
	assert.Empty(t, reference.FallbackWarnings())
}

func TestSessionProxyFallback_TwiceDroppedConnectionThenFailingPerSessionStartup(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{Metadata: malformedManifest})
	args := testkit.SharedLaunchArgs(contentDir)
	sessionArgs := testkit.SharedLaunchArgs(contentDir, "--server-mode", "session")
	accepted := h.BreakBeforeReady(h.Identity(args), 0, testkit.CloseConnection)

	l := testkit.StartLaunch(t, h.SessionProxyParams(args), h.Binary, args...)
	proxyErr := l.Wait(testkit.SharedServerDeadline)
	referenceErr := testkit.StartLaunch(t, h.SessionProxyParams(sessionArgs), h.Binary, sessionArgs...).
		Wait(testkit.SharedServerDeadline)

	require.Error(t, proxyErr)
	require.Error(t, referenceErr)
	assert.Equal(t, referenceErr.Error(), proxyErr.Error())
	assert.Equal(t, 2, accepted(), "a broken connection re-enters discovery exactly once")
	assert.Zero(t, h.Spawns(), "nothing respawns after the second break")
	warnings := l.FallbackWarnings()
	require.Len(t, warnings, 1, "stderr: %s", l.Stderr())
	assert.Contains(t, warnings[0], "cause=dropped_twice")
	assert.Empty(t, string(l.ReadToEOF()))
}

func TestSessionProxyFallback_TwiceClosedConnectionServesHeldRequests(t *testing.T) {
	requireTwiceBrokenConnectionFallsBack(t, testkit.CloseConnection)
}

func TestSessionProxyFallback_TwiceInvalidReadyLineServesHeldRequests(t *testing.T) {
	requireTwiceBrokenConnectionFallsBack(t, testkit.SendInvalidReadyLine)
}

func requireTwiceBrokenConnectionFallsBack(t *testing.T, brk testkit.ConnectionBreak) {
	t.Helper()
	h := testkit.NewSharedServerHarness(t)
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t))
	l, gate := testkit.StartHeldLaunch(t, h.SessionProxyParams(args), h.Binary, args...)
	accepted := h.BreakBeforeReady(h.Identity(args), 0, brk)

	requireFallbackAnswersHeldRequests(t, l, gate, "dropped_twice")

	assert.Equal(t, 2, accepted())
	assert.Zero(t, h.Spawns())
	assert.Zero(t, h.Builds())
}

func TestSessionProxyFallback_SingleBreakAttachesToASharedServerWithoutFallback(t *testing.T) {
	breaks := map[string]testkit.ConnectionBreak{
		"closed connection":  testkit.CloseConnection,
		"invalid ready line": testkit.SendInvalidReadyLine,
	}
	for name, brk := range breaks {
		t.Run(name, func(t *testing.T) {
			args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t))
			var accepted func() int
			h, l := attachWithoutLingerLoss(t, func(h *testkit.SharedServerHarness) *testkit.LaunchUnderTest {
				accepted = h.BreakBeforeReady(h.Identity(args), 1, brk)
				l, gate := testkit.StartHeldLaunch(t, h.SessionProxyParams(args), h.Binary, args...)
				writeHeldRequests(l)
				require.Empty(t, l.FallbackWarnings(), "discovery is still held")

				gate.Open()
				requireHeldRequestsAnswered(t, l)
				return l
			})

			assert.Empty(t, l.FallbackWarnings())
			assert.Equal(t, 1, accepted())
			assert.Equal(t, 1, h.Builds())
			proxyAttachedSession(t, l)
		})
	}
}

// attachWithoutLingerLoss repeats attempt, each on a fresh harness, until one
// does not lose to the initial linger, and returns that attempt's harness and
// launch.
func attachWithoutLingerLoss(t *testing.T, attempt func(h *testkit.SharedServerHarness) *testkit.LaunchUnderTest) (*testkit.SharedServerHarness, *testkit.LaunchUnderTest) {
	t.Helper()
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	for attempts := 1; ; attempts++ {
		h := testkit.NewSharedServerHarness(t)
		l := attempt(h)
		if !lostToTheInitialLinger(t, h, l, deadline) {
			return h, l
		}
		require.True(t, time.Now().Before(deadline), "no attempt attached without lingering out in %d attempts", attempts)
	}
}

// lostToTheInitialLinger classifies an attempt whose shared server answered
// or whose fallback answered. A won attempt has its single built candidate
// still attached; a lost one had a built candidate linger out after bind
// before the session proxy reached it (#196), which may show as a fallback
// or as a second build. Anything else fails the test.
func lostToTheInitialLinger(t *testing.T, h *testkit.SharedServerHarness, l *testkit.LaunchUnderTest, deadline time.Time) bool {
	t.Helper()
	if h.Builds() == 1 && len(l.FallbackWarnings()) == 0 && len(builtExits(h)) == 0 {
		return false
	}
	exits := awaitBuiltExits(h, deadline, 1)
	require.NotEmpty(t, exits, "a lost attempt must have a built candidate that lingered out")
	if verdict, failedCheck := checkLingerExits(coldStartEvidence{exits: exits}); failedCheck {
		require.FailNow(t, "a lost attempt's candidate did not linger out", verdict.reason)
	}
	return true
}

func TestSessionProxyFallback_ServerModeSharedExplicitOrDefaultFallsBackAndExitsNormally(t *testing.T) {
	contentDir := newSessionProxyContentDir(t)
	launches := map[string][]string{
		"explicit shared": testkit.SharedLaunchArgs(contentDir),
		"default": {
			"--content-dir", contentDir,
			"--transport", "stdio",
			"--shared-server-linger", "1s",
			"--shared-server-connect-wait", "5s",
		},
	}
	for name, args := range launches {
		t.Run(name, func(t *testing.T) {
			h := testkit.NewSharedServerHarness(t)
			params := h.SessionProxyParams(args)
			params.SpawnSharedServer = func(string) error { return errors.New("spawn refused by the test") }
			sessionArgs := append(slices.Clone(args), "--server-mode", "session")

			l, gate := testkit.StartHeldLaunch(t, params, h.Binary, args...)
			requireFallbackAnswersHeldRequests(t, l, gate, "spawn")
			l.CloseStdin()
			proxyErr := l.Wait(testkit.SharedServerDeadline)

			reference := testkit.StartLaunch(t, h.SessionProxyParams(sessionArgs), h.Binary, sessionArgs...)
			writeHeldRequests(reference)
			requireHeldRequestsAnswered(t, reference)
			reference.CloseStdin()
			referenceErr := reference.Wait(testkit.SharedServerDeadline)

			require.NoError(t, proxyErr)
			require.NoError(t, referenceErr)
			assert.Equal(t, referenceErr, proxyErr)
		})
	}
}

func TestSessionProxyFallback_LogsToTheLaunchStderrOnlyWithoutASessionAttribute(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t))
	params := h.SessionProxyParams(args)
	params.SpawnSharedServer = func(string) error { return errors.New("spawn refused by the test") }

	l, gate := testkit.StartHeldLaunch(t, params, h.Binary, args...)
	requireFallbackAnswersHeldRequests(t, l, gate, "spawn")
	l.CloseStdin()
	require.NoError(t, l.Wait(testkit.SharedServerDeadline))

	lines := strings.Split(l.Stderr(), "\n")
	for _, line := range lines {
		assert.NotContains(t, line, " session=")
	}
	warningAt := slices.IndexFunc(lines, func(line string) bool {
		return strings.Contains(line, `msg="Shared server unavailable, continuing as a per-session server"`)
	})
	require.GreaterOrEqual(t, warningAt, 0, "stderr: %s", l.Stderr())
	assert.True(t, slices.ContainsFunc(lines[warningAt+1:], func(line string) bool {
		return strings.Contains(line, `msg="Registered tool"`)
	}), "the per-session server logs its startup after the warning; stderr: %s", l.Stderr())

	logFiles, err := os.ReadDir(h.LogDir)
	require.NoError(t, err)
	assert.Empty(t, logFiles, "the fallback writes no shared server log")
	if h.EndpointDirExists() {
		endpointEntries, err := os.ReadDir(h.EndpointDir)
		require.NoError(t, err)
		assert.Empty(t, endpointEntries, "the fallback creates no socket or lock")
	}
}

func writeHeldRequests(l *testkit.LaunchUnderTest) {
	l.WriteLine(rawInitializeRequest)
	l.WriteLine(rawInitializedNotice)
	l.WriteLine(rawSearchCallRequestID2)
}

func requireHeldRequestsAnswered(t *testing.T, l *testkit.LaunchUnderTest) {
	t.Helper()
	initialize := decodeRawResponse(t, l.ReadLine())
	search := decodeRawResponse(t, l.ReadLine())
	assert.Equal(t, "1", string(initialize.ID), "initialize is answered first")
	assert.Nil(t, initialize.Error)
	assert.Contains(t, string(initialize.Result), `"serverInfo"`)
	assert.Equal(t, "2", string(search.ID))
	assert.Nil(t, search.Error)
	assert.Contains(t, string(search.Result), "Authentication Guide", "the search is answered from the content")
	assert.NotContains(t, string(search.Result), `"isError":true`)
}
