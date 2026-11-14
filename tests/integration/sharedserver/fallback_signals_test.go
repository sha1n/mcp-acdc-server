//go:build unix

package sharedserver

import (
	"errors"
	"os"
	"os/signal"
	"syscall"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionProxyFallback_SignalAfterTheHandoverIsNoLongerHandledByTheLaunch(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			h := testkit.NewSharedServerHarness(t)
			args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t))
			params := h.SessionProxyParams(args)
			params.SpawnSharedServer = func(string) error { return errors.New("spawn refused by the test") }
			l, gate := testkit.StartHeldLaunch(t, params, h.Binary, args...)
			requireFallbackAnswersHeldRequests(t, l, gate, "spawn")

			// The guard stands in for the default disposition a per-session
			// launch has, which would end the test process.
			guard := make(chan os.Signal, 1)
			signal.Notify(guard, sig)
			defer signal.Stop(guard)
			require.NoError(t, syscall.Kill(os.Getpid(), sig))
			<-guard

			l.WriteLine(rawToolsListRequestID3)
			resp := decodeRawResponse(t, l.ReadLine())
			assert.Equal(t, "3", string(resp.ID))
			assert.Nil(t, resp.Error)
			l.CloseStdin()
			assert.NoError(t, l.Wait(testkit.SharedServerDeadline), "the per-session server ends at stdin EOF, not at the signal")
		})
	}
}
