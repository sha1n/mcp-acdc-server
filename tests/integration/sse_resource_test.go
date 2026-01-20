package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/sha1n/mcp-acdc-server-go/internal/app"
	"github.com/sha1n/mcp-acdc-server-go/internal/config"
)

// TestSSEResourceListAndRead tests that resources can be listed and read via SSE transport
// This reproduces an issue where Gemini CLI cannot fetch resources via SSE
func TestSSEResourceListAndRead(t *testing.T) {
	// 1. Prepare content directory with a test resource
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	if err := os.MkdirAll(resourcesDir, 0755); err != nil {
		t.Fatal(err)
	}

	metadata := `server:
  name: test-sse
  version: 1.0.0
  instructions: Test SSE server
tools:
  - name: search
    description: Search content
`
	if err := os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadata), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a test resource
	resourceContent := `---
name: Test SSE Resource
description: A test resource for SSE debugging
---
# Test SSE Resource

This is SSE test content.
`
	if err := os.WriteFile(filepath.Join(resourcesDir, "test-resource.md"), []byte(resourceContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Configure and Create Server (In-Process)
	settings := &config.Settings{
		ContentDir: contentDir,
		Transport:  "sse",
		Host:       "localhost",
		Port:       0, // will fail to start if 0, need a free port
		Search: config.SearchSettings{
			InMemory:   true,
			MaxResults: 10,
		},
	}
	// We'll override port later

	mcpServer, cleanup, err := app.CreateMCPServer(settings)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer cleanup()

	// 3. Start SSE Server
	sse := server.NewSSEServer(mcpServer)

	port, err := getFreePort()
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}

	// Start in goroutine
	go func() {
		if err := sse.Start(fmt.Sprintf(":%d", port)); err != nil && err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()

	// Wait for server to start
	time.Sleep(200 * time.Millisecond)

	baseURL := fmt.Sprintf("http://localhost:%d", port)

	// 4. Connect to SSE endpoint and get session
	t.Logf("Connecting to SSE endpoint at %s...", baseURL)
	sseResp, err := http.Get(baseURL + "/sse")
	if err != nil {
		t.Fatalf("Failed to connect to SSE: %v", err)
	}
	defer func() {
		_ = sseResp.Body.Close()
	}()

	if sseResp.StatusCode != 200 {
		t.Fatalf("SSE connection failed with status: %d", sseResp.StatusCode)
	}

	// Read the endpoint event to get the message URL
	buf := make([]byte, 4096)
	n, err := sseResp.Body.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("Failed to read SSE: %v", err)
	}

	sseData := string(buf[:n])
	t.Logf("SSE response: %s", sseData)

	// Parse the endpoint from SSE data
	// Format: event: endpoint\ndata: /message?sessionId=xxx\n\n
	var messageEndpoint string
	for _, line := range strings.Split(sseData, "\n") {
		if strings.HasPrefix(line, "data: ") {
			messageEndpoint = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			break
		}
	}

	if messageEndpoint == "" {
		t.Fatalf("Failed to extract message endpoint from SSE: %s", sseData)
	}

	t.Logf("Message endpoint: %s", messageEndpoint)

	// Build full message URL
	messageURL := baseURL + messageEndpoint

	// Helper to send JSON-RPC request
	sendRequest := func(id int, method string, params interface{}) (map[string]interface{}, error) {
		req := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  method,
			"params":  params,
		}
		reqBytes, _ := json.Marshal(req)
		// t.Logf("Sending request to %s: %s", messageURL, reqBytes)

		resp, err := http.Post(messageURL, "application/json", bytes.NewReader(reqBytes))
		if err != nil {
			return nil, fmt.Errorf("POST failed: %w", err)
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		if resp.StatusCode != 200 && resp.StatusCode != 202 {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
		}

		// For SSE, response comes via the SSE stream, not the POST response
		// Read from SSE stream
		buf := make([]byte, 8192)
		n, err := sseResp.Body.Read(buf)
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("failed to read SSE response: %w", err)
		}

		sseData := string(buf[:n])
		// t.Logf("SSE response data: %s", sseData)

		// Parse SSE message format: event: message\ndata: {...}\n\n
		var jsonData string
		for _, line := range strings.Split(sseData, "\n") {
			if strings.HasPrefix(line, "data: ") {
				jsonData = strings.TrimPrefix(line, "data: ")
				break
			}
		}

		if jsonData == "" {
			return nil, fmt.Errorf("no JSON data in SSE response: %s", sseData)
		}

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &result); err != nil {
			return nil, fmt.Errorf("failed to parse JSON: %w, data: %s", err, jsonData)
		}

		return result, nil
	}

	// 5. Send initialize request
	initResp, err := sendRequest(1, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]string{
			"name":    "test-client",
			"version": "1.0",
		},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	if initResp["error"] != nil {
		t.Fatalf("Initialize returned error: %v", initResp["error"])
	}

	result, ok := initResp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("Invalid initialize result: %v", initResp)
	}

	caps, ok := result["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatalf("Missing capabilities: %v", result)
	}

	t.Logf("SSE Server capabilities: %v", caps)

	if caps["resources"] == nil {
		t.Error("SSE Server does not advertise resources capability!")
	}

	// 6. Send resources/list request
	listResp, err := sendRequest(2, "resources/list", map[string]interface{}{})
	if err != nil {
		t.Fatalf("resources/list failed: %v", err)
	}

	if listResp["error"] != nil {
		t.Fatalf("resources/list returned error: %v", listResp["error"])
	}

	listResult, ok := listResp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("Invalid resources/list result: %v", listResp)
	}

	resourcesList, ok := listResult["resources"].([]interface{})
	if !ok || len(resourcesList) == 0 {
		t.Fatalf("No resources in list: %v", listResult)
	}

	t.Logf("SSE: Found %d resources", len(resourcesList))

	// 7. Send resources/read request
	readResp, err := sendRequest(3, "resources/read", map[string]interface{}{
		"uri": "acdc://test-resource",
	})
	if err != nil {
		t.Fatalf("resources/read failed: %v", err)
	}

	if readResp["error"] != nil {
		t.Fatalf("resources/read returned error: %v", readResp["error"])
	}

	readResult, ok := readResp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("Invalid resources/read result: %v", readResp)
	}

	contents, ok := readResult["contents"].([]interface{})
	if !ok || len(contents) == 0 {
		t.Fatalf("No contents in read result: %v", readResult)
	}

	content := contents[0].(map[string]interface{})
	text, ok := content["text"].(string)
	if !ok || text == "" {
		t.Fatalf("Missing text content: %v", content)
	}

	t.Logf("SSE: Read resource content: %s", text[:min(50, len(text))])
	t.Log("SSE resource operations succeeded!")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
