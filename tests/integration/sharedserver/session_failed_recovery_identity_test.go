//go:build unix

package sharedserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The recovery computes the identity before anything else, so no outage line
// can be established as held before it fails; the in-flight search is
// answered at the crash and must not be answered again.
func TestSessionFailedRecovery_UncomputableIdentityFailsWithoutFallback(t *testing.T) {
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	contentDir := newSessionProxyContentDir(t)
	manifest := filepath.Join(contentDir, ".acdc", "config.yaml")
	h := testkit.NewSharedServerHarness(t)
	held := h.HoldSearches()
	l := h.Launch(recoveryLaunchArgs(contentDir)...)
	attachRecords(t, l, 1, deadline)

	l.WriteLine(modernSearch("5", testkit.HeldSearchQuery))
	held.AwaitReceived()
	require.NoError(t, os.Chmod(manifest, 0o000))
	t.Cleanup(func() {
		if err := os.Chmod(manifest, 0o644); err != nil {
			t.Errorf("failed to restore the manifest mode: %v", err)
		}
	})
	h.StopSharedServers()

	require.Equal(t, connectionLostError("5"), string(l.ReadLine()))
	assert.Empty(t, string(l.ReadToEOF()), "the in-flight search is answered once")
	requireFailedRecovery(t, h, l, "identity")
	warnings := stderrRecords(t, l, slogtext.Record{"level": "WARN", "msg": crashWarningMessage})
	require.Len(t, warnings, 1)
	assert.Equal(t, "1", warnings[0]["in_flight"])
	assert.Equal(t, 1, h.Spawns(), "the recovery failed before dial-or-spawn")
	assert.Equal(t, 1, h.Builds())
}
