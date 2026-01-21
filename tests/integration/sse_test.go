package integration

import (
	"net/http"
	"testing"

	"github.com/sha1n/mcp-acdc-server-go/tests/integration/testkit"
)

func TestSSEServer(t *testing.T) {
	// 1. Setup Data using testkit helper
	contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"res1.md": "---\nname: Test\ndescription: Desc\n---\nContent",
		},
	})

	// 2. Start ACDC Service via TestEnv using testkit flags helper
	flags := testkit.NewTestFlags(t, contentDir, nil)
	service := testkit.NewACDCService("acdc", flags)
	env := testkit.NewTestEnv(service)

	props, err := env.Start()
	if err != nil {
		t.Fatalf("Failed to start env: %v", err)
	}
	defer func() { _ = env.Stop() }()

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
