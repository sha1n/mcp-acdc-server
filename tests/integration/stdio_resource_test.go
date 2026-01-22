package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResourceReadIntegration(t *testing.T) {
	// 1. Build the server binary
	tempDir := t.TempDir()
	binPath := filepath.Join(tempDir, "mcp-server")

	rootDir, err := filepath.Abs("../../")
	if err != nil {
		t.Fatalf("Failed to get root dir: %v", err)
	}
	cmdPath := filepath.Join(rootDir, "cmd", "acdc-mcp")

	buildCmd := exec.Command("go", "build", "-o", binPath, cmdPath)
	buildCmd.Dir = rootDir
	out, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to build server: %v\nOutput: %s", err, out)
	}

	// 2. Prepare content
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	toolsDir := filepath.Join(resourcesDir, "tools") // Subdirectory to match user case
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte("server:\n  name: test\n  version: 1.0\n  instructions: inst\ntools: []\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Add the specific resource mentioned by user
	resourceContent := "---\nname: Bert Benchmarking Tool\ndescription: A tool\n---\nHere is the content of the tool."
	if err := os.WriteFile(filepath.Join(toolsDir, "bert-benchmarking.md"), []byte(resourceContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Run Server
	serverCmd := exec.Command(binPath)
	serverCmd.Env = append(os.Environ(),
		"ACDC_MCP_TRANSPORT=stdio",
		fmt.Sprintf("ACDC_MCP_CONTENT_DIR=%s", contentDir),
	)

	stdin, err := serverCmd.StdinPipe()
	if err != nil {
		t.Fatalf("Failed to get stdin: %v", err)
	}
	stdout, err := serverCmd.StdoutPipe()
	if err != nil {
		t.Fatalf("Failed to get stdout: %v", err)
	}

	// Capture stderr
	stderrPipe, err := serverCmd.StderrPipe()
	if err != nil {
		t.Fatalf("Failed to get stderr: %v", err)
	}

	if err := serverCmd.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	defer func() {
		if err := serverCmd.Process.Kill(); err != nil {
			t.Logf("Failed to kill server: %v", err)
		}
	}()

	// Read stderr in background
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			t.Logf("Server Stderr: %s", scanner.Text())
		}
	}()

	scanner := bufio.NewScanner(stdout)

	sendRequest := func(req interface{}) {
		reqBytes, _ := json.Marshal(req)
		if _, err := fmt.Fprintf(stdin, "%s\n", reqBytes); err != nil {
			t.Fatalf("Failed to write to stdin: %v", err)
		}
	}

	readResponse := func(expectedID int) map[string]interface{} {
		for scanner.Scan() {
			line := scanner.Text()
			// t.Logf("Server Stdout: %s", line)
			var resp map[string]interface{}
			if err := json.Unmarshal([]byte(line), &resp); err == nil {
				if id, ok := resp["id"].(float64); ok && int(id) == expectedID {
					return resp
				}
			}
		}
		return nil
	}

	// 4. Initialize
	sendRequest(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]string{
				"name":    "test-client",
				"version": "1.0",
			},
		},
	})

	initResp := readResponse(1)
	if initResp == nil {
		t.Fatal("Failed to get initialize response")
	}

	// 5. Send initialized notification
	sendRequest(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})

	// 6. List Resources to verify exact URI string
	targetURI := "acdc://tools/bert-benchmarking"
	sendRequest(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      10,
		"method":  "resources/list",
	})

	listResp := readResponse(10)
	if listResp == nil {
		t.Fatal("Failed to get resources/list response")
	}

	listResult, ok := listResp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("resources/list result is not a map")
	}

	resourcesList, ok := listResult["resources"].([]interface{})
	if !ok {
		t.Fatalf("resources list is invalid")
	}

	found := false
	for _, r := range resourcesList {
		resMap := r.(map[string]interface{})
		uri := resMap["uri"].(string)
		t.Logf("Advertised Resource URI: %s", uri)
		if uri == targetURI {
			found = true
		}
	}
	if !found {
		t.Errorf("Target URI %s not found in resources/list", targetURI)
	}

	// 7. Read Resource
	sendRequest(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "resources/read",
		"params": map[string]string{
			"uri": targetURI,
		},
	})

	readResp := readResponse(2)
	if readResp == nil {
		t.Fatal("Failed to get read resource response")
	}

	result, ok := readResp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("Response result is not a map: %v", readResp)
	}

	contents, ok := result["contents"].([]interface{})
	if !ok || len(contents) == 0 {
		errorInfo := readResp["error"]
		t.Fatalf("Failed to get contents. Error in response: %v", errorInfo)
	}

	firstContent := contents[0].(map[string]interface{})
	text := firstContent["text"].(string)

	if text != "Here is the content of the tool." {
		t.Errorf("Unexpected content: %s", text)
	} else {
		t.Logf("Successfully read resource content: %s", text)
	}
}
