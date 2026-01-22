package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestStdioServer(t *testing.T) {
	// 1. Build the server binary
	tempDir := t.TempDir()
	binPath := filepath.Join(tempDir, "mcp-server")

	// Assuming running from module root or adjusting path
	// We are in tests/integration. Module root is ../..
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

	// 2. Prepare content for valid startup
	// Server expects content dir. Default is ./content in config.
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	if err := os.MkdirAll(resourcesDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte("server:\n  name: test\n  version: 1.0\n  instructions: inst\ntools: []\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Add a dummy resource
	if err := os.WriteFile(filepath.Join(resourcesDir, "res1.md"), []byte("---\nname: Test\ndescription: Desc\n---\nContent"), 0644); err != nil {
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
	// Kill is deferred later loop call

	// Read stderr in background
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			t.Logf("Server Stderr: %s", scanner.Text())
		}
	}()

	// 4. Send Initialize Request
	// JSON-RPC 2.0
	req := map[string]interface{}{
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
	}
	reqBytes, _ := json.Marshal(req)

	// Write with newline
	if _, err := fmt.Fprintf(stdin, "%s\n", reqBytes); err != nil {
		t.Fatalf("Failed to write to stdin: %v", err)
	}

	// 5. Read Response
	defer func() {
		if err := serverCmd.Process.Kill(); err != nil {
			t.Logf("Failed to kill server: %v", err)
		}
	}()
	// The server might emit log lines to Stderr, but JSON-RPC to Stdout.
	// We need to read line by line.
	scanner := bufio.NewScanner(stdout)

	// Set a timeout?
	done := make(chan bool)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			// Parse JSON
			var resp map[string]interface{}
			if err := json.Unmarshal([]byte(line), &resp); err == nil {
				// Check if it's the response to id 1
				if id, ok := resp["id"].(float64); ok && id == 1 {
					// Validate result
					if result, ok := resp["result"].(map[string]interface{}); ok {
						if proto, ok := result["protocolVersion"].(string); ok && proto == "2024-11-05" {
							done <- true
							return
						}
					}
				}
			}
		}
		close(done)
	}()

	select {
	case result := <-done:
		if !result {
			t.Error("Did not receive valid initialize response")
		}
	case <-time.After(2 * time.Second):
		t.Error("Timeout waiting for response")
	}
}
