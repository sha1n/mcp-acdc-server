package integration

import (
	"context"
	"fmt"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInitializeReturnsServerInfo verifies that initialize response contains
// server name and version from mcp-metadata.yaml (P-INIT-02, META-01)
func TestInitializeReturnsServerInfo(t *testing.T) {
	metadata := `server:
  name: My Custom Server
  version: 2.5.0
  instructions: Custom server instructions for testing
tools: []
`
	client := testkit.NewStdioTestClient(t, &testkit.ContentDirOptions{
		Metadata: metadata,
	})
	defer client.Close()

	// Get initialize result
	initResult := client.InitializeResult()
	require.NotNil(t, initResult)

	// Verify server info
	assert.Equal(t, "My Custom Server", initResult.ServerInfo.Name, "server name should match metadata")
	assert.Equal(t, "2.5.0", initResult.ServerInfo.Version, "server version should match metadata")
}

// TestInitializeReturnsCapabilities verifies that initialize response contains
// correct capabilities for tools, resources, and prompts (P-INIT-03)
func TestInitializeReturnsCapabilities(t *testing.T) {
	client := testkit.NewStdioTestClient(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"test.md": "---\nname: Test\ndescription: Test\n---\nContent",
		},
		Prompts: map[string]string{
			"prompt.md": "---\nname: test-prompt\ndescription: A test prompt\narguments: []\n---\nHello",
		},
	})
	defer client.Close()

	// Get initialize result
	initResult := client.InitializeResult()
	require.NotNil(t, initResult)

	// Verify capabilities are advertised
	caps := initResult.Capabilities
	assert.NotNil(t, caps.Tools, "should advertise tools capability")
	assert.NotNil(t, caps.Resources, "should advertise resources capability")
	assert.NotNil(t, caps.Prompts, "should advertise prompts capability")
}

// TestToolDescriptionOverride verifies that tool descriptions from mcp-metadata.yaml
// override the defaults (META-02)
func TestToolDescriptionOverride(t *testing.T) {
	customSearchDesc := "My custom search description for testing override"
	customReadDesc := "My custom read description for testing override"

	metadata := fmt.Sprintf(`server:
  name: test-override
  version: 1.0.0
  instructions: Test server
tools:
  - name: search
    description: "%s"
  - name: read
    description: "%s"
`, customSearchDesc, customReadDesc)

	client := testkit.NewStdioTestClient(t, &testkit.ContentDirOptions{
		Metadata: metadata,
		Resources: map[string]string{
			"test.md": "---\nname: Test\ndescription: Test\n---\nContent",
		},
	})
	defer client.Close()

	ctx := context.Background()

	// List tools
	result, err := client.ListTools(ctx)
	require.NoError(t, err)

	// Build map of tool descriptions
	toolDescs := make(map[string]string)
	for _, tool := range result.Tools {
		toolDescs[tool.Name] = tool.Description
	}

	assert.Equal(t, customSearchDesc, toolDescs["search"], "search tool description should be overridden")
	assert.Equal(t, customReadDesc, toolDescs["read"], "read tool description should be overridden")
}

// TestDefaultToolDescriptions verifies that default tool descriptions are used
// when not overridden in metadata
func TestDefaultToolDescriptions(t *testing.T) {
	// Metadata with no tool overrides
	metadata := `server:
  name: test-defaults
  version: 1.0.0
  instructions: Test server
tools: []
`
	client := testkit.NewStdioTestClient(t, &testkit.ContentDirOptions{
		Metadata: metadata,
		Resources: map[string]string{
			"test.md": "---\nname: Test\ndescription: Test\n---\nContent",
		},
	})
	defer client.Close()

	ctx := context.Background()

	// List tools
	result, err := client.ListTools(ctx)
	require.NoError(t, err)

	for _, tool := range result.Tools {
		switch tool.Name {
		case "search":
			assert.Contains(t, tool.Description, "Search", "search tool should have default description")
			assert.Contains(t, tool.Description, "full-text", "search tool default should mention full-text search")
		case "read":
			assert.Contains(t, tool.Description, "Read", "read tool should have default description")
			assert.Contains(t, tool.Description, "resource", "read tool default should mention resource")
		}
	}
}
