package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server-go/tests/integration/testkit"
)

// TestSSEResourceListAndRead tests that resources can be listed and read via SSE transport
func TestSSEResourceListAndRead(t *testing.T) {
	// 1. Prepare content directory with a test resource using testkit helper
	resourceContent := `---
name: Test SSE Resource
description: A test resource for SSE debugging
---
# Test SSE Resource

This is SSE test content.
`
	metadata := `server:
  name: test-sse
  version: 1.0.0
  instructions: Test SSE server
tools:
  - name: search
    description: Search content
`
	contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Metadata: metadata,
		Resources: map[string]string{
			"test-resource.md": resourceContent,
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

		// Read from SSE stream
		buf := make([]byte, 8192)
		n, err := sseResp.Body.Read(buf)
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("failed to read SSE response: %w", err)
		}

		sseData := string(buf[:n])
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
