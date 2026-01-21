package integration

import (
	"net/http"
	"testing"

	"github.com/sha1n/mcp-acdc-server-go/tests/integration/testkit"
)

func TestAPIKeyAuthIntegration(t *testing.T) {
	contentDir := testkit.CreateTestContentDir(t, nil)

	tests := []struct {
		name       string
		apiKey     string
		wantStatus int
	}{
		{"no key returns 401", "", http.StatusUnauthorized},
		{"wrong key returns 401", "wrong-key", http.StatusUnauthorized},
		{"key-A returns 200", "key-A", http.StatusOK},
		{"key-B returns 200", "key-B", http.StatusOK},
	}

	// Setup server with API key auth
	flags := testkit.NewTestFlags(t, contentDir, &testkit.FlagOptions{AuthType: "apikey"})
	_ = flags.Set("auth-api-keys", "key-A,key-B")

	env := testkit.NewTestEnv(testkit.NewACDCService("acdc", flags))
	props, err := env.Start()
	if err != nil {
		t.Fatalf("Failed to start env: %v", err)
	}
	defer func() { _ = env.Stop() }()

	baseURL := props["acdc.baseURL"].(string)
	url := baseURL + "/sse"

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{}
			req, _ := http.NewRequest("GET", url, nil)
			if tt.apiKey != "" {
				req.Header.Set("X-API-Key", tt.apiKey)
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("Expected status %d, got %d", tt.wantStatus, resp.StatusCode)
			}
		})
	}
}

func TestBasicAuthIntegration(t *testing.T) {
	contentDir := testkit.CreateTestContentDir(t, nil)

	tests := []struct {
		name       string
		username   string
		password   string
		wantStatus int
	}{
		{"no credentials returns 401", "", "", http.StatusUnauthorized},
		{"wrong password returns 401", "user", "wrong", http.StatusUnauthorized},
		{"wrong username returns 401", "wrong", "password", http.StatusUnauthorized},
		{"correct credentials return 200", "user", "password", http.StatusOK},
	}

	// Setup server with basic auth
	flags := testkit.NewTestFlags(t, contentDir, &testkit.FlagOptions{AuthType: "basic"})
	_ = flags.Set("auth-basic-username", "user")
	_ = flags.Set("auth-basic-password", "password")

	env := testkit.NewTestEnv(testkit.NewACDCService("acdc", flags))
	props, err := env.Start()
	if err != nil {
		t.Fatalf("Failed to start env: %v", err)
	}
	defer func() { _ = env.Stop() }()

	baseURL := props["acdc.baseURL"].(string)
	url := baseURL + "/sse"

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{}
			req, _ := http.NewRequest("GET", url, nil)
			if tt.username != "" || tt.password != "" {
				req.SetBasicAuth(tt.username, tt.password)
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("Expected status %d, got %d", tt.wantStatus, resp.StatusCode)
			}
		})
	}
}
