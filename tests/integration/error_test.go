package integration

import (
	"context"
	"os"
	"path/filepath"
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

// A repository still on the mcp-* layout must fail to start with a message
// naming both the stale path and its replacement. That string is the entire
// user-facing migration path, so it is asserted end to end and not only at the
// unit boundary.
//
// env.Start() surfaces startup errors on SSE, not on stdio, so this test uses
// the default SSE transport.
func TestStartup_RefusesLegacyLayout(t *testing.T) {
	contentDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"),
		[]byte(testkit.DefaultMetadata()), 0o600))

	flags := testkit.NewTestFlags(t, contentDir, nil)
	env := testkit.NewTestEnv(testkit.NewACDCService("legacy-layout-test", flags))

	_, err := env.Start()

	require.Error(t, err)
	require.Contains(t, err.Error(), "mcp-metadata.yaml")
	require.Contains(t, err.Error(), ".acdc/config.yaml")
}
