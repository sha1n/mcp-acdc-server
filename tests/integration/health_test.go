package integration

import (
	"io"
	"net/http"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

func TestHealthEndpoint(t *testing.T) {
	// 1. Setup Data using testkit helper
	contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"res1.md": "---\nname: Test\ndescription: Desc\n---\nContent",
		},
	})

	// 2. Start ACDC Service with API Key Auth enabled
	apiKey := "test-api-key"
	flags := testkit.NewTestFlags(t, contentDir, nil)
	_ = flags.Set("auth-type", "apikey")
	_ = flags.Set("auth-api-keys", apiKey)

	service := testkit.NewACDCService("acdc", flags)
	env := testkit.NewTestEnv(service)

	props, err := env.Start()
	if err != nil {
		t.Fatalf("Failed to start env: %v", err)
	}
	t.Cleanup(func() { require.NoError(t, env.Stop()) })

	baseURL := props["acdc.baseURL"].(string)

	t.Run("Health endpoint is accessible without auth", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/health")
		if err != nil {
			t.Fatalf("Failed to request health: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200 OK, got %d", resp.StatusCode)
		}

		ct := resp.Header.Get("Content-Type")
		if ct != "text/plain; charset=utf-8" {
			t.Errorf("Expected text/plain; charset=utf-8, got %s", ct)
		}

		body, _ := io.ReadAll(resp.Body)
		if string(body) != "ok" {
			t.Errorf("Expected body 'ok', got '%s'", string(body))
		}
	})

	t.Run("Other endpoints require auth", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/sse")
		if err != nil {
			t.Fatalf("Failed to request SSE: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("Expected 401 Unauthorized for /sse without key, got %d", resp.StatusCode)
		}
	})
}
