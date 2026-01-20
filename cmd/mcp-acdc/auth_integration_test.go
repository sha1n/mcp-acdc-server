package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/sha1n/mcp-acdc-server-go/internal/auth"
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

// waitForServer polls the server until it responds or times out
func waitForServer(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 100 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("server did not become ready")
}

// startTestServer starts an HTTP server and returns a shutdown function
func startTestServer(t *testing.T, settings *config.Settings, mcpServer *server.MCPServer) (baseURL string, shutdown func()) {
	t.Helper()

	sseServer := server.NewSSEServer(mcpServer)
	authMiddleware, err := auth.NewMiddleware(settings.Auth)
	if err != nil {
		t.Fatalf("Failed to create auth middleware: %v", err)
	}
	handler := authMiddleware(sseServer)

	addr := fmt.Sprintf("%s:%d", settings.Host, settings.Port)
	srv := &http.Server{Addr: addr, Handler: handler}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()

	baseURL = fmt.Sprintf("http://localhost:%d", settings.Port)
	if err := waitForServer(baseURL+"/sse", 5*time.Second); err != nil {
		t.Fatalf("Server did not start: %v", err)
	}

	return baseURL, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
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
			Type:    config.AuthTypeAPIKey,
			APIKeys: []string{"key-A", "key-B"},
		},
	}

	// Create a dummy MCP server
	mcpServer := server.NewMCPServer("test", "1.0")

	// Start Server with proper shutdown
	baseURL, shutdown := startTestServer(t, settings, mcpServer)
	defer shutdown()

	url := fmt.Sprintf("%s/sse", baseURL)

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
			Type: config.AuthTypeBasic,
			Basic: config.BasicAuthSettings{
				Username: "user",
				Password: "password",
			},
		},
	}

	mcpServer := server.NewMCPServer("test", "1.0")

	// Start Server with proper shutdown
	baseURL, shutdown := startTestServer(t, settings, mcpServer)
	defer shutdown()

	url := fmt.Sprintf("%s/sse", baseURL)

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
