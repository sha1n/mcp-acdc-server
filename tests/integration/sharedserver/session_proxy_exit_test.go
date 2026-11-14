//go:build unix

package sharedserver

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

const prompterExit = 500 * time.Millisecond

func TestSessionProxy_StdinEOFDuringDiscoveryEndsTheSessionPromptly(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t), "--shared-server-connect-wait", "1m")
	h.HoldLock(h.Identity(args))
	l := h.Launch(args...)
	h.Eventually(func() bool { return h.Spawns() >= 1 }, "the proxy is discovering")

	l.CloseStdin()

	require.NoError(t, l.Wait(prompterExit))
}

func TestSessionProxy_StdinEOFAfterAttachEndsTheSessionCleanly(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	l := h.Launch(testkit.SharedLaunchArgs(newSessionProxyContentDir(t))...)
	session := connectAndListTools(t, l)

	require.NoError(t, session.Close())

	require.NoError(t, l.Wait(testkit.SharedServerDeadline))
}

func TestSessionProxy_SignalEndsTheSessionPromptlyInEveryPhase(t *testing.T) {
	phases := []struct {
		name  string
		enter func(t *testing.T, h *testkit.SharedServerHarness, args []string) *testkit.LaunchUnderTest
	}{
		{
			name: "discovery",
			enter: func(t *testing.T, h *testkit.SharedServerHarness, args []string) *testkit.LaunchUnderTest {
				h.HoldLock(h.Identity(args))
				l := h.Launch(args...)
				h.Eventually(func() bool { return h.Spawns() >= 1 }, "the proxy is discovering")
				return l
			},
		},
		{
			name: "readiness wait",
			enter: func(t *testing.T, h *testkit.SharedServerHarness, args []string) *testkit.LaunchUnderTest {
				accepted := h.ListenWithoutReady(h.Identity(args))
				l := h.Launch(args...)
				h.Eventually(func() bool { return accepted() >= 1 }, "the proxy is awaiting the ready line")
				require.Equal(t, 0, h.Spawns())
				return l
			},
		},
		{
			name: "attached",
			enter: func(t *testing.T, h *testkit.SharedServerHarness, args []string) *testkit.LaunchUnderTest {
				received := h.ServeReadyWithoutAnswering(h.Identity(args))
				l := h.Launch(args...)
				l.WriteLine(rawToolsListRequestID2)
				h.Eventually(func() bool { return received() >= 1 }, "the proxy relays host input")
				require.Equal(t, 0, h.Spawns())
				return l
			},
		},
	}
	for _, phase := range phases {
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
			t.Run(phase.name+"/"+sig.String(), func(t *testing.T) {
				h := testkit.NewSharedServerHarness(t)
				args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t), "--shared-server-connect-wait", "1m")
				l := phase.enter(t, h, args)
				// The guard keeps the signal from taking its default action on
				// the test process once the proxy stops listening for it.
				guard := make(chan os.Signal, 1)
				signal.Notify(guard, sig)
				defer signal.Stop(guard)

				require.NoError(t, syscall.Kill(os.Getpid(), sig))

				require.NoError(t, l.Wait(prompterExit))
			})
		}
	}
}
