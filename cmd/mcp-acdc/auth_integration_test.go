package main

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/sha1n/mcp-acdc-server-go/internal/config"
)

// GetFreePort returns a free port from the kernel
func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = l.Close()
	}()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func TestAuthIntegration(t *testing.T) {
	port, err := getFreePort()
	if err != nil {
		t.Fatalf("Failed to get port: %v", err)
	}

	settings := &config.Settings{
		Host: "localhost",
		Port: port,
		Auth: config.AuthSettings{
			Type:    "apikey",
			APIKeys: []string{"key-A", "key-B"},
		},
	}

	// Create a dummy MCP server
	mcpServer := server.NewMCPServer("test", "1.0")

	// Start Server
	go func() {
		// Use StartSSEServer which has the middleware wiring
		if err := StartSSEServer(mcpServer, settings); err != nil && err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()
	time.Sleep(100 * time.Millisecond) // Wait for start

	url := fmt.Sprintf("http://localhost:%d/sse", port)

	// Case 1: No Key -> 401
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("Failed to call server: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected 401 for no key, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Case 2: Wrong Key -> 401
	client := &http.Client{}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("X-API-Key", "wrong")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Failed to call server: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected 401 for wrong key, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Case 3: Key A -> 200
	req, _ = http.NewRequest("GET", url, nil)
	req.Header.Set("X-API-Key", "key-A")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Failed to call server: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 for key-A, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Case 4: Key B -> 200
	req, _ = http.NewRequest("GET", url, nil)
	req.Header.Set("X-API-Key", "key-B")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Failed to call server: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 for key-B, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestBasicAuthIntegration(t *testing.T) {
	port, err := getFreePort()
	if err != nil {
		t.Fatalf("Failed to get port: %v", err)
	}

	settings := &config.Settings{
		Host: "localhost",
		Port: port,
		Auth: config.AuthSettings{
			Type: "basic",
			Basic: config.BasicAuthSettings{
				Username: "user",
				Password: "password",
			},
		},
	}

	mcpServer := server.NewMCPServer("test", "1.0")

	go func() {
		if err := StartSSEServer(mcpServer, settings); err != nil && err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()
	time.Sleep(100 * time.Millisecond)

	url := fmt.Sprintf("http://localhost:%d/sse", port)

	// Case 1: No Creds -> 401
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("Failed to call server: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected 401 for missing creds, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Case 2: Wrong Creds -> 401
	req, _ := http.NewRequest("GET", url, nil)
	req.SetBasicAuth("user", "wrong")
	client := &http.Client{}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Failed to call server: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected 401 for wrong pass, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Case 3: Correct Creds -> 200
	req, _ = http.NewRequest("GET", url, nil)
	req.SetBasicAuth("user", "password")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Failed to call server: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 for correct creds, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}
