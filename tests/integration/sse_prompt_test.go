package integration

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPromptGetViaSSE tests prompts/get via SSE transport (P-PRM-03)
func TestPromptGetViaSSE(t *testing.T) {
	promptContent := `---
name: greeting-prompt
description: A greeting prompt for SSE testing
arguments:
  - name: name
    description: Name to greet
    required: true
---
Hello {{.name}}, welcome to the SSE test!`

	client := testkit.NewSSETestClient(t, &testkit.ContentDirOptions{
		Prompts: map[string]string{
			"greeting.md": promptContent,
		},
	})
	defer client.Close()

	ctx := context.Background()

	// Test prompts/list via SSE
	t.Run("prompts/list via SSE", func(t *testing.T) {
		result, err := client.ListPrompts(ctx)
		require.NoError(t, err)
		require.Len(t, result.Prompts, 1)

		prompt := result.Prompts[0]
		assert.Equal(t, "greeting-prompt", prompt.Name)
		assert.Equal(t, "A greeting prompt for SSE testing", prompt.Description)
	})

	// Test prompts/get via SSE
	t.Run("prompts/get via SSE", func(t *testing.T) {
		result, err := client.GetPrompt(ctx, "greeting-prompt", map[string]string{
			"name": "SSE User",
		})
		require.NoError(t, err)
		require.Len(t, result.Messages, 1)

		msg := result.Messages[0]
		textContent, ok := msg.Content.(*mcp.TextContent)
		require.True(t, ok, "content should be text")
		assert.Equal(t, "Hello SSE User, welcome to the SSE test!", textContent.Text)
	})
}
