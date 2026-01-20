package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/sha1n/mcp-acdc-server-go/internal/config"
	"github.com/sha1n/mcp-acdc-server-go/tests/testutils"
)

func TestStartSSEServer_TLS(t *testing.T) {
	// Generate self-signed cert
	tempDir := t.TempDir()
	certFile := filepath.Join(tempDir, "cert.pem")
	keyFile := filepath.Join(tempDir, "key.pem")

	err := testutils.GenerateSelfSignedCert(certFile, keyFile)
	if err != nil {
		t.Fatalf("Failed to generate cert: %v", err)
	}

	// Create a random port
	port := 50000 + (time.Now().UnixNano() % 10000)

	settings := &config.Settings{
		Host:     "127.0.0.1",
		Port:     int(port),
		CertFile: certFile,
		KeyFile:  keyFile,
		Auth: config.AuthSettings{
			Type: "none",
		},
	}

	mcpServer := server.NewMCPServer("test", "1.0")

	// Start server in goroutine
	go func() {
		if err := StartSSEServer(mcpServer, settings); err != nil && err != http.ErrServerClosed {
			// It's hard to fail the test from here, but we can log
			// In a real scenario we might use a channel to signal error
			t.Logf("Server stopped with error: %v", err)
		}
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Make HTTPS request
	caCert, _ := os.ReadFile(certFile)
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: caCertPool,
			},
		},
	}

	url := fmt.Sprintf("https://127.0.0.1:%d/sse", port)
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("Failed to make HTTPS request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}
