package integration

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/sha1n/mcp-acdc-server-go/internal/domain"
	"github.com/sha1n/mcp-acdc-server-go/internal/mcp"
	"github.com/sha1n/mcp-acdc-server-go/internal/resources"
	"github.com/sha1n/mcp-acdc-server-go/internal/search"
)

type StubSearcher struct{}

func (s *StubSearcher) Search(queryStr string, limit *int) ([]search.SearchResult, error) {
	return []search.SearchResult{}, nil
}
func (s *StubSearcher) IndexDocuments(docs []search.Document) error { return nil }
func (s *StubSearcher) Close()                                      {}

func TestSSEServer(t *testing.T) {
	// 1. Setup Data
	tempDir := t.TempDir()
	resFile := filepath.Join(tempDir, "res1.md")
	if err := os.WriteFile(resFile, []byte("---\nname: Test\ndescription: Desc\n---\nContent"), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Setup Dependencies
	metadata := domain.McpMetadata{
		Server: domain.ServerMetadata{
			Name: "test", Version: "1.0", Instructions: "inst",
		},
		Tools: []domain.ToolMetadata{{Name: "search", Description: "Search"}},
	}

	resDefs := []resources.ResourceDefinition{
		{
			URI: "file:///res1", Name: "Test", Description: "Desc",
			MIMEType: "text/markdown", FilePath: resFile,
		},
	}
	resProvider := resources.NewResourceProvider(resDefs)
	searcher := &StubSearcher{}

	// 3. Create Server
	s := mcp.CreateServer(metadata, resProvider, searcher)

	// 4. Start SSE Server
	sse := server.NewSSEServer(s)

	portToUse, err := getFreePort()
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}
	url := fmt.Sprintf("http://localhost:%d/sse", portToUse)

	// Start in goroutine
	go func() {
		if err := sse.Start(fmt.Sprintf(":%d", portToUse)); err != nil && err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()

	// Give it a moment to start
	time.Sleep(100 * time.Millisecond)

	// 5. Connect
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("Failed to connect to SSE: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Logf("Failed to close body: %v", err)
		}
	}()

	if resp.StatusCode != 200 {
		t.Errorf("Expected 200 OK, got %d", resp.StatusCode)
	}

	// Basic check that it is SSE
	ct := resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Expected text/event-stream, got %s", ct)
	}

	// We could also test POST /messages but requires a session ID?
	// MCP SSE protocol: client connects to /sse, gets an endpoint /messages?sessionId=...
	// We'd need to parse the first event.
}

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
