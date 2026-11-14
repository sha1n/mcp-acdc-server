//go:build unix

package sharedserver

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSessionProxyContentDir(t *testing.T) string {
	t.Helper()
	return testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"auth-guide.md": "---\nname: Authentication Guide\ndescription: Guide for authentication\n---\nThis document covers authentication and security best practices.",
		},
	})
}

func TestSessionProxy_RelaysInitializeToolsListAndSearch(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t))
	ctx, cancel := context.WithTimeout(context.Background(), testkit.SharedServerDeadline)
	defer cancel()

	session := h.Launch(args...).Connect(ctx)

	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, tools.Tools)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search", Arguments: map[string]any{"query": "authentication"}})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotEmpty(t, result.Content)
	assert.Contains(t, result.Content[0].(*mcp.TextContent).Text, "Authentication Guide")
	assert.Equal(t, 1, h.Builds(), "exactly one candidate becomes the shared server")
}

func TestSessionProxy_SecondProxyAttachesToTheSameSharedServer(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t))
	ctx, cancel := context.WithTimeout(context.Background(), testkit.SharedServerDeadline)
	defer cancel()

	first := h.Launch(args...).Connect(ctx)
	spawnsBeforeSecond := h.Spawns()

	second := h.Launch(args...).Connect(ctx)

	for _, session := range []*mcp.ClientSession{first, second} {
		_, err := session.ListTools(ctx, nil)
		require.NoError(t, err)
	}
	assert.Equal(t, spawnsBeforeSecond, h.Spawns(), "the second proxy spawns nothing")
	assert.Equal(t, 1, h.Builds(), "both sessions are served by one shared server")
}
