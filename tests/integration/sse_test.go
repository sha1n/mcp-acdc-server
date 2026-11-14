package integration

import (
	"net/http"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

func TestSSEServer(t *testing.T) {
	for _, variant := range serverModeFlagVariants {
		t.Run(variant.name, func(t *testing.T) {
			testSSEServer(t, variant.flags)
		})
	}
}

func testSSEServer(t *testing.T, serverModeFlags map[string]string) {
	// 1. Setup Data using testkit helper
	contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"res1.md": "---\nname: Test\ndescription: Desc\n---\nContent",
		},
	})

	// 2. Start ACDC Service via TestEnv using testkit flags helper
	flags := testkit.NewTestFlags(t, contentDir, nil)
	for name, value := range serverModeFlags {
		require.NoError(t, flags.Set(name, value))
	}
	service := testkit.NewACDCService("acdc", flags)
	env := testkit.NewTestEnv(service)

	props, err := env.Start()
	if err != nil {
		t.Fatalf("Failed to start env: %v", err)
	}
	t.Cleanup(func() { require.NoError(t, env.Stop()) })

	baseURL := props["acdc.baseURL"].(string)
	url := baseURL + "/sse"

	// 3. Connect
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("Failed to connect to SSE: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Logf("Failed to close body: %v", err)
		}
	}()

	if resp.StatusCode != 200 {
		t.Errorf("Expected 200 OK, got %d", resp.StatusCode)
	}

	// Basic check that it is SSE
	ct := resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Expected text/event-stream, got %s", ct)
	}
}
