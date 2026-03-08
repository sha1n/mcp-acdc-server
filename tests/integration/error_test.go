package integration

import (
	"context"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

// TestResourceReadUnknownURI verifies that resources/read returns proper error
// for unknown URI (P-RES-03)
func TestResourceReadUnknownURI(t *testing.T) {
	client := testkit.NewStdioTestClient(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"existing.md": "---\nname: Existing\ndescription: An existing resource\n---\nContent",
		},
	})
	defer client.Close()

	ctx := context.Background()

	// Try to read a non-existent resource
	_, err := client.ReadResource(ctx, "acdc://nonexistent-resource")

	// Should return an error
	require.Error(t, err, "should return error for unknown resource")
}

// TestPromptGetUnknownPrompt verifies that prompts/get returns proper error
// for unknown prompt
func TestPromptGetUnknownPrompt(t *testing.T) {
	client := testkit.NewStdioTestClient(t, &testkit.ContentDirOptions{
		Prompts: map[string]string{
			"existing.md": "---\nname: existing-prompt\ndescription: An existing prompt\narguments: []\n---\nHello",
		},
	})
	defer client.Close()

	ctx := context.Background()

	// Try to get a non-existent prompt
	_, err := client.GetPrompt(ctx, "nonexistent-prompt", nil)

	// Should return an error
	require.Error(t, err, "should return error for unknown prompt")
}
