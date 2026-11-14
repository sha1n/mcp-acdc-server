//go:build unix

package sharedserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/sha1n/mcp-acdc-server/internal/testutil/slogtext"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	originalInstructions = "original instructions"
	editedInstructions   = "edited instructions"
	originalPromptName   = "original-prompt"
	editedPromptName     = "edited-prompt"
)

func manifestWithInstructions(instructions string) string {
	return "server:\n  name: test\n  version: \"1.0\"\n  instructions: " + instructions + "\ntools: []\n"
}

func promptNamed(name string) string {
	return "---\nname: " + name + "\ndescription: A test prompt\narguments: []\n---\nHello"
}

func newStartupFixedContentDir(t *testing.T) string {
	t.Helper()
	return testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Metadata: manifestWithInstructions(originalInstructions),
		Resources: map[string]string{
			"guide.md": "---\nname: Guide\ndescription: A guide\n---\nGuide body.",
		},
		Prompts: map[string]string{"prompt.md": promptNamed(originalPromptName)},
	})
}

func editManifest(t *testing.T, contentDir string) {
	t.Helper()
	cp := content.NewContentProvider(contentDir)
	require.NoError(t, os.WriteFile(cp.ConfigFile, []byte(manifestWithInstructions(editedInstructions)), 0o644))
}

func editPrompt(t *testing.T, contentDir string) {
	t.Helper()
	cp := content.NewContentProvider(contentDir)
	require.NoError(t, os.WriteFile(filepath.Join(cp.PromptsDir, "prompt.md"), []byte(promptNamed(editedPromptName)), 0o644))
}

// runWinningColdStarts generalises runWinningColdStart to attempts that cold
// start builds shared servers by design: every build beyond those must be a
// candidate that lingered out before any session reached it (#196).
func runWinningColdStarts(t *testing.T, builds int, attempt coldStartAttempt) *testkit.SharedServerHarness {
	t.Helper()
	deadline := time.Now().Add(testkit.SharedServerDeadline)
	for attempts := 1; ; attempts++ {
		h := testkit.NewSharedServerHarness(t)
		attempt(t, h)
		if h.Builds() <= builds {
			return h
		}
		requireLostColdStarts(t, h, builds)
		require.True(t, time.Now().Before(deadline), "no attempt built only %d shared servers in %d attempts", builds, attempts)
	}
}

func requireLostColdStarts(t *testing.T, h *testkit.SharedServerHarness, builds int) {
	t.Helper()
	lingeredOut := 0
	for _, exit := range h.SharedServerExits() {
		if !exit.Built {
			continue
		}
		require.NoError(t, exit.Err, "a lost attempt's candidate must end as a normal linger exit")
		require.GreaterOrEqual(t, exit.ExitedAt.Sub(exit.SpawnedAt), config.MinSharedServerLinger,
			"a lost attempt's candidate exited before the linger elapsed")
		lingeredOut++
	}
	require.Equal(t, h.Builds()-builds, lingeredOut, "every extra build follows a candidate that lingered out")
}

func promptNames(t *testing.T, ctx context.Context, session *mcp.ClientSession) []string {
	t.Helper()
	result, err := session.ListPrompts(ctx, nil)
	require.NoError(t, err)
	names := make([]string, 0, len(result.Prompts))
	for _, p := range result.Prompts {
		names = append(names, p.Name)
	}
	return names
}

func TestSessionProxy_StartupFixedContentEditGivesLaterLaunchesANewSharedServer(t *testing.T) {
	tests := []struct {
		name               string
		edit               func(*testing.T, string)
		before, after      string
		instructionsChange bool
	}{
		{name: "manifest", edit: editManifest, before: originalInstructions, after: editedInstructions, instructionsChange: true},
		{name: "prompt file", edit: editPrompt, before: originalPromptName, after: editedPromptName},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testkit.SharedServerDeadline)
			defer cancel()
			var first, second *mcp.ClientSession
			var args []string
			var idBefore, idAfter string
			var spawnsBeforeSecond int

			h := runWinningColdStarts(t, 2, func(t *testing.T, h *testkit.SharedServerHarness) {
				args = testkit.SharedLaunchArgs(newStartupFixedContentDir(t))
				first = h.Launch(args...).Connect(ctx)
				idBefore = h.Identity(args)
				tt.edit(t, args[1])
				idAfter = h.Identity(args)
				spawnsBeforeSecond = h.Spawns()
				second = h.Launch(args...).Connect(ctx)
			})

			require.NotEqual(t, idBefore, idAfter, "the edit must change the server identity")
			assert.Greater(t, h.Spawns(), spawnsBeforeSecond, "a launch after the edit must not attach to the existing shared server")
			assert.Equal(t, 2, h.Builds(), "each identity gets its own shared server")

			for _, id := range []string{idBefore, idAfter} {
				socketPath, err := h.Endpoint().SocketPath(id)
				require.NoError(t, err)
				assert.FileExists(t, socketPath, "both shared servers must be running")
			}

			if tt.instructionsChange {
				assert.Equal(t, tt.before, first.InitializeResult().Instructions, "a running session keeps its shared server")
				assert.Equal(t, tt.after, second.InitializeResult().Instructions)
			}
			for _, session := range []*mcp.ClientSession{first, second} {
				_, err := session.ListTools(ctx, nil)
				require.NoError(t, err, "both shared servers must serve their sessions concurrently")
			}
			if !tt.instructionsChange {
				assert.Equal(t, []string{tt.before}, promptNames(t, ctx, first), "a running session keeps its shared server")
				assert.Equal(t, []string{tt.after}, promptNames(t, ctx, second))
			}
		})
	}
}

func TestSharedServer_IdentityMismatchIsLoggedAndExitsWithoutBinding(t *testing.T) {
	const credential = "mismatch-test-secret-key"
	h := testkit.NewSharedServerHarness(t)
	args := testkit.SharedLaunchArgs(newStartupFixedContentDir(t), "--auth-type", "apikey", "--auth-api-keys", credential)
	staleID := h.Identity(args)
	editManifest(t, args[1])
	require.NotEqual(t, staleID, h.Identity(args))

	require.NoError(t, h.SpawnSharedServer(args)(staleID))

	h.Eventually(func() bool { return len(h.SharedServerExits()) == 1 }, "the mismatched candidate must exit")
	exit := h.SharedServerExits()[0]
	require.ErrorIs(t, exit.Err, sharedserver.ErrIdentityMismatch)
	assert.False(t, exit.Built, "nothing may be built for a mismatched identity")
	socketPath, err := h.Endpoint().SocketPath(staleID)
	require.NoError(t, err)
	assert.NoFileExists(t, socketPath, "a mismatched candidate must not bind")
	h.HoldLock(staleID)()

	logged, err := os.ReadFile(filepath.Join(h.LogDir, staleID+".log"))
	require.NoError(t, err)
	assert.NotContains(t, string(logged), credential, "the log must not disclose credentials")
	records, err := slogtext.Parse(logged)
	require.NoError(t, err)
	failures := slogtext.Filter(records, slogtext.Record{"level": "ERROR", "msg": "Identity self-check failed"})
	require.Len(t, failures, 1, "the self-check failure must be logged exactly once")
	assert.Equal(t, staleID, failures[0]["identity"], "the entry must name the expected identity")
	assert.Equal(t, h.Identity(args), failures[0]["content_identity"], "the entry must name the identity of the content on disk")
}

func TestSessionProxy_LaunchesDifferingInSearchInMemoryDoNotShare(t *testing.T) {
	var spawnsBeforeSecond int

	h := runWinningColdStarts(t, 2, func(t *testing.T, h *testkit.SharedServerHarness) {
		args := testkit.SharedLaunchArgs(newStartupFixedContentDir(t))
		t.Setenv("ACDC_MCP_SEARCH_IN_MEMORY", "true")
		connectAndListTools(t, h.Launch(args...))
		spawnsBeforeSecond = h.Spawns()
		t.Setenv("ACDC_MCP_SEARCH_IN_MEMORY", "false")
		connectAndListTools(t, h.Launch(args...))
	})

	assert.Greater(t, h.Spawns(), spawnsBeforeSecond, "a launch with another search.in_memory must not attach")
	assert.Equal(t, 2, h.Builds())
}

func TestSessionProxy_LaunchesDifferingOnlyInAuthOrUISettingsShare(t *testing.T) {
	tests := []struct {
		name  string
		extra []string
	}{
		{name: "auth", extra: []string{"--auth-type", "apikey", "--auth-api-keys", "test-key"}},
		{name: "ui", extra: []string{"--ui-enabled", "--ui-max-results", "7"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var spawnsBeforeSecond int

			h := runWinningColdStart(t, func(t *testing.T, h *testkit.SharedServerHarness) {
				contentDir := newStartupFixedContentDir(t)
				base := testkit.SharedLaunchArgs(contentDir)
				other := testkit.SharedLaunchArgs(contentDir, tt.extra...)
				connectAndListTools(t, h.Launch(base...))
				spawnsBeforeSecond = h.Spawns()
				connectAndListTools(t, h.Launch(other...))
			})

			assert.Equal(t, spawnsBeforeSecond, h.Spawns(), "a launch differing only in %s settings must attach", tt.name)
			assert.Equal(t, 1, h.Builds())
		})
	}
}
