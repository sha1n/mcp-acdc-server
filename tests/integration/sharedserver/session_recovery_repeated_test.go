//go:build unix

package sharedserver

import (
	"strconv"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionRecovery_TwoProxiesOnACrashedSharedServerRecoverOntoOneNewSharedServer(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	args := recoveryLaunchArgs(newSessionProxyContentDir(t))
	h := testkit.NewSharedServerHarness(t)
	a := h.Launch(args...)
	firstA := attachRecords(t, a, 1, deadline)[0]
	b := h.Launch(args...)
	firstB := attachRecords(t, b, 1, deadline)[0]

	logPath := firstA["log"]
	require.Equal(t, logPath, firstB["log"], "both sessions attach to the shared server of one identity")
	require.Equal(t, 1, h.Builds(), "both sessions share the first shared server")
	requireDistinct(t, []string{firstA["session"], firstB["session"]}, "first sessions")
	firstLives := awaitSessionsByServerLife(t, logPath, 2, deadline)
	require.Len(t, firstLives, 1)
	require.ElementsMatch(t, []string{firstA["session"], firstB["session"]}, firstLives[0], "the first shared server served both sessions")

	h.StopSharedServers()

	secondA := attachRecords(t, a, 2, deadline)[1]
	secondB := attachRecords(t, b, 2, deadline)[1]
	assert.Equal(t, logPath, secondA["log"])
	assert.Equal(t, logPath, secondB["log"])
	assert.Equal(t, 2, h.Builds(), "exactly one new shared server was built for both recoveries")
	requireDistinct(t, []string{firstA["session"], firstB["session"], secondA["session"], secondB["session"]}, "sessions")
	lives := awaitSessionsByServerLife(t, logPath, 4, deadline)
	require.Len(t, lives, 2, "one shared server life per build")
	assert.ElementsMatch(t, []string{firstA["session"], firstB["session"]}, lives[0])
	assert.ElementsMatch(t, []string{secondA["session"], secondB["session"]}, lives[1], "both sessions recovered onto the second shared server")

	requireModernSearchSucceeds(t, a, "31")
	requireModernSearchSucceeds(t, b, "32")
	assert.Zero(t, h.FallbackWarnings())
}

func TestSessionRecovery_SuccessiveCrashesRecoverEachTime(t *testing.T) {
	const crashes = 3
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	args := recoveryLaunchArgs(newSessionProxyContentDir(t))
	h := testkit.NewSharedServerHarness(t)
	held := h.HoldSearches()
	l := h.Launch(args...)
	attachRecords(t, l, 1, deadline)

	for crash := 1; crash <= crashes; crash++ {
		heldID := strconv.Itoa(crash * 10)
		l.WriteLine(modernSearch(heldID, testkit.HeldSearchQuery))
		held.AwaitReceived()
		h.StopSharedServers()

		require.Equal(t, connectionLostError(heldID), string(l.ReadLine()), "crash %d: the in-flight search gets the connection-lost error", crash)
		requireModernSearchSucceeds(t, l, strconv.Itoa(crash*10+1))
	}

	attaches := attachRecords(t, l, crashes+1, deadline)
	require.Len(t, attaches, crashes+1)
	sessions := sessionsOf(attaches)
	requireDistinct(t, sessions, "attached sessions")
	assert.Equal(t, crashes+1, h.Builds(), "each crash built one new shared server")
	logPath := attaches[0]["log"]
	for _, attach := range attaches {
		assert.Equal(t, logPath, attach["log"])
	}
	// A shared server logs its startup entry before it builds, so the build
	// count bounds the startup entries already written.
	assert.Len(t, serverLogRecords(t, logPath, slogtext.Record{"msg": serverStartupMessage}), crashes+1, "one startup entry per shared server life")
	lives := awaitSessionsByServerLife(t, logPath, crashes+1, deadline)
	require.Len(t, lives, crashes+1)
	for i, life := range lives {
		assert.Equal(t, []string{sessions[i]}, life, "shared server life %d served exactly the session attached to it", i)
	}
	assert.Zero(t, h.FallbackWarnings())
}

func TestSessionRecovery_StderrHasOneCrashWarningAndOneRecoveryInfoPerCrashAndOneAttachLinePerAttach(t *testing.T) {
	inFlightPerCrash := []int{1, 2}
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	args := recoveryLaunchArgs(newSessionProxyContentDir(t))
	h := testkit.NewSharedServerHarness(t)
	held := h.HoldSearches()
	l := h.Launch(args...)
	logPath := attachRecords(t, l, 1, deadline)[0]["log"]

	for crash, inFlight := range inFlightPerCrash {
		ids := make([]string, inFlight)
		for i := range ids {
			ids[i] = strconv.Itoa((crash+1)*10 + i)
			l.WriteLine(modernSearch(ids[i], testkit.HeldSearchQuery))
		}
		for range ids {
			held.AwaitReceived()
		}
		h.StopSharedServers()

		for _, id := range ids {
			require.Equal(t, connectionLostError(id), string(l.ReadLine()), "crash %d", crash+1)
		}
		awaitStderrRecords(t, l, slogtext.Record{"level": "INFO", "msg": recoveredMessage}, crash+1, deadline)
	}
	l.CloseStdin()
	require.NoError(t, l.Wait(testkit.SharedServerDeadline), "the launch ends normally at host stdin EOF")

	crashCount := len(inFlightPerCrash)
	warnings := stderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage})
	require.Len(t, warnings, crashCount, "one crash warning per crash")
	for i, warning := range warnings {
		assert.Equal(t, strconv.Itoa(inFlightPerCrash[i]), warning["in_flight"], "crash %d warning counts the requests answered with the connection-lost error", i+1)
	}
	assert.Len(t, stderrRecords(t, l, slogtext.Record{"level": "INFO", "msg": recoveredMessage}), crashCount, "one recovery entry per crash")
	assert.Len(t, stderrRecords(t, l, slogtext.Record{"level": "INFO", "msg": attachMessage}), crashCount+1, "one attach line per attach")

	want := []string{attachMessage}
	for range inFlightPerCrash {
		want = append(want, crashWarningMessage, attachMessage, recoveredMessage)
	}
	assert.Equal(t, want, recoveryLifecycleOf(t, l), "each crash warns, attaches again, then reports the recovery")
	requireNoProxyRecordsInServerLog(t, logPath)
	assert.Zero(t, h.FallbackWarnings())
}

func requireModernSearchSucceeds(t *testing.T, l *testkit.LaunchUnderTest, id string) {
	t.Helper()
	l.WriteLine(modernSearch(id, "authentication"))
	resp := decodeRawResponse(t, l.ReadLine())
	require.JSONEq(t, id, string(resp.ID))
	require.Nil(t, resp.Error, "search %s succeeds", id)
	require.NotEmpty(t, resp.Result)
}

// awaitSessionsByServerLife returns, once the shared server log at path
// holds attached sessions in total, the sessions each server life recorded
// as attached, one group per startup entry in log order: every server life
// of one identity appends to the same log file.
func awaitSessionsByServerLife(t *testing.T, path string, attached int, deadline time.Time) [][]string {
	t.Helper()
	awaitServerLogRecords(t, path, slogtext.Record{"msg": sessionAttachedMessage}, attached, deadline)
	var lives [][]string
	for _, record := range serverLogRecords(t, path, slogtext.Record{}) {
		switch record["msg"] {
		case serverStartupMessage:
			lives = append(lives, []string{})
		case sessionAttachedMessage:
			require.NotEmpty(t, lives, "a session attached before any shared server started")
			lives[len(lives)-1] = append(lives[len(lives)-1], record["session"])
		}
	}
	return lives
}

// recoveryLifecycleOf returns the messages of the launch's crash, attach and
// recovery entries, in the order it wrote them.
func recoveryLifecycleOf(t *testing.T, l *testkit.LaunchUnderTest) []string {
	t.Helper()
	var lifecycle []string
	for _, record := range stderrRecords(t, l, slogtext.Record{}) {
		switch record["msg"] {
		case crashWarningMessage, attachMessage, recoveredMessage:
			lifecycle = append(lifecycle, record["msg"])
		}
	}
	return lifecycle
}
