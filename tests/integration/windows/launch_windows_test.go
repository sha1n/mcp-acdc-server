package windows

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const launchTimeout = 30 * time.Second

type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func buildBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "acdc-mcp.exe")
	build := exec.Command("go", "build", "-o", binary, "github.com/sha1n/mcp-acdc-server/cmd/acdc-mcp")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "go build failed: %s", output)
	return binary
}

// isolatedUserDirs gives the launch its own profile, cache, roaming and temp
// directories, so anything a shared server would create lands where the test
// can see it.
func isolatedUserDirs(t *testing.T) map[string]string {
	t.Helper()
	dirs := map[string]string{}
	for _, name := range []string{"HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "TMP", "TEMP"} {
		dirs[name] = t.TempDir()
	}
	return dirs
}

// launchEnv drops inherited ACDC_MCP_* settings so only the flags under test
// configure the launch.
func launchEnv(dirs map[string]string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "ACDC_MCP_") {
			env = append(env, kv)
		}
	}
	for name, dir := range dirs {
		env = append(env, name+"="+dir)
	}
	return env
}

func TestWindowsLaunch_SharedModeServesAsAPerSessionServerWithoutArtifacts(t *testing.T) {
	binary := buildBinary(t)
	contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"guide.md": "---\nname: Guide\ndescription: Windows guide\n---\n# Guide\n\nThe unique term windowsprobe appears here.\n",
		},
	})
	dirs := isolatedUserDirs(t)

	cmd := exec.Command(binary, "--content-dir", contentDir, "--server-mode", "shared")
	cmd.Env = launchEnv(dirs)
	var stderr lockedBuffer
	cmd.Stderr = &stderr

	ctx, cancel := context.WithTimeout(context.Background(), launchTimeout)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "windows-test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	require.NoError(t, err, "initialize failed; stderr:\n%s", stderr.String())
	require.NotNil(t, session.InitializeResult())

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"query": "windowsprobe"},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	require.NotEmpty(t, result.Content)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "guide")

	require.NoError(t, session.Close())
	require.NotNil(t, cmd.ProcessState)
	assert.Equal(t, 0, cmd.ProcessState.ExitCode())

	assert.NotContains(t, stderr.String(), "level=WARN")
	for name, dir := range dirs {
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Empty(t, entries, "%s=%s must stay empty: no socket, lock file or log directory", name, dir)
	}
}
