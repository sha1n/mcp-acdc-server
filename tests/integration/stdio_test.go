package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
)

func TestStdioServer(t *testing.T) {
	// 1. Prepare content using testkit
	contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"res1.md": "---\nname: Test\ndescription: Desc\n---\nContent",
		},
	})

	// 2. Start ACDC Service with stdio transport
	flags := testkit.NewTestFlags(t, contentDir, &testkit.FlagOptions{
		Transport: "stdio",
	})

	service := testkit.NewACDCService("acdc-stdio", flags)
	env := testkit.NewTestEnv(service)

	props, err := env.Start()
	if err != nil {
		t.Fatalf("Failed to start env: %v", err)
	}
	defer func() { _ = env.Stop() }()

	stdin := props["acdc.stdin"].(io.Writer)
	stdout := props["acdc.stdout"].(io.Reader)

	// 3. Send Initialize Request
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

	if _, err := fmt.Fprintf(stdin, "%s\n", reqBytes); err != nil {
		t.Fatalf("Failed to write to stdin: %v", err)
	}

	// 4. Read Response
	scanner := bufio.NewScanner(stdout)
	done := make(chan bool)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			var resp map[string]interface{}
			if err := json.Unmarshal([]byte(line), &resp); err == nil {
				if id, ok := resp["id"].(float64); ok && id == 1 {
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
	case <-time.After(5 * time.Second):
		t.Error("Timeout waiting for response")
	}
}

func TestStdioServer_HTTPPortNotListening(t *testing.T) {
	// 1. Prepare content
	contentDir := testkit.CreateTestContentDir(t, nil)

	// 2. Start ACDC Service in stdio mode with a specific port
	testPort := testkit.MustGetFreePort(t)
	flags := testkit.NewTestFlags(t, contentDir, &testkit.FlagOptions{
		Transport: "stdio",
		Port:      testPort,
	})

	service := testkit.NewACDCService("acdc-stdio-port", flags)
	env := testkit.NewTestEnv(service)

	props, err := env.Start()
	if err != nil {
		t.Fatalf("Failed to start env: %v", err)
	}
	defer func() { _ = env.Stop() }()

	stdin := props["acdc.stdin"].(io.Writer)
	stdout := props["acdc.stdout"].(io.Reader)

	// 3. Verify server is running by sending initialize
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
		},
	}
	reqBytes, _ := json.Marshal(req)
	_, _ = fmt.Fprintf(stdin, "%s\n", reqBytes)

	scanner := bufio.NewScanner(stdout)
	serverReady := make(chan bool)
	go func() {
		for scanner.Scan() {
			var resp map[string]interface{}
			if err := json.Unmarshal([]byte(scanner.Bytes()), &resp); err == nil {
				if id, ok := resp["id"].(float64); ok && id == 1 {
					serverReady <- true
					return
				}
			}
		}
		serverReady <- false
	}()

	select {
	case ready := <-serverReady:
		if !ready {
			t.Fatal("Server not ready")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Timeout")
	}

	// 4. Verify HTTP port is NOT listening
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://localhost:%d/health", testPort))
	if err == nil {
		_ = resp.Body.Close()
		t.Errorf("HTTP port %d is listening when it should NOT be (got status %d)", testPort, resp.StatusCode)
	} else {
		t.Logf("Verified: HTTP port %d is not listening (expected error: %v)", testPort, err)
	}
}
