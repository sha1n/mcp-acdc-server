package testkit

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestClient wraps an MCP ClientSession for testing via stdio or SSE transport.
type TestClient struct {
	Session    *mcp.ClientSession
	client     *mcp.Client
	env        TestEnv
	t          testing.TB
	cancelFunc context.CancelFunc // for SSE transport to cancel the connection context
}

// NewStdioTestClient creates a test client connected to an ACDC server via stdio transport.
// It starts the server, creates an MCP client, and connects them via pipes.
func NewStdioTestClient(t testing.TB, contentOpts *ContentDirOptions) *TestClient {
	t.Helper()

	contentDir := CreateTestContentDir(t, contentOpts)

	flags := NewTestFlags(t, contentDir, &FlagOptions{
		Transport: "stdio",
	})

	service := NewACDCService("acdc-client-test", flags)
	env := NewTestEnv(service)

	props, err := env.Start()
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	stdin := props["acdc.stdin"].(io.WriteCloser)
	stdout := props["acdc.stdout"].(io.ReadCloser)

	// Create MCP client
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}, nil)

	// Create transport using the pipes
	transport := &mcp.IOTransport{
		Reader: stdout,
		Writer: stdin,
	}

	// Connect client to server
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		_ = env.Stop()
		t.Fatalf("Failed to connect client: %v", err)
	}

	return &TestClient{
		Session: session,
		client:  client,
		env:     env,
		t:       t,
	}
}

// NewSSETestClient creates a test client connected to an ACDC server via SSE transport.
// It starts the server, creates an MCP client, and connects via SSE.
func NewSSETestClient(t testing.TB, contentOpts *ContentDirOptions) *TestClient {
	t.Helper()

	contentDir := CreateTestContentDir(t, contentOpts)

	flags := NewTestFlags(t, contentDir, nil) // defaults to SSE

	service := NewACDCService("acdc-sse-client-test", flags)
	env := NewTestEnv(service)

	props, err := env.Start()
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	baseURL := props["acdc.baseURL"].(string)
	sseURL := baseURL + "/sse"

	// Create MCP client
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}, nil)

	// Create SSE transport
	transport := &mcp.SSEClientTransport{
		Endpoint: sseURL,
	}

	// Connect client to server
	// The SSE transport uses the context for the entire connection lifecycle,
	// so we must NOT cancel it until Close() is called
	ctx, cancel := context.WithCancel(context.Background())
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		cancel()
		_ = env.Stop()
		t.Fatalf("Failed to connect SSE client: %v", err)
	}

	return &TestClient{
		Session:    session,
		client:     client,
		env:        env,
		t:          t,
		cancelFunc: cancel,
	}
}

// Close stops the client and server
func (tc *TestClient) Close() {
	if tc.cancelFunc != nil {
		tc.cancelFunc()
	}
	if tc.Session != nil {
		_ = tc.Session.Close()
	}
	if tc.env != nil {
		_ = tc.env.Stop()
	}
}

// InitializeResult returns the server's initialize response
func (tc *TestClient) InitializeResult() *mcp.InitializeResult {
	return tc.Session.InitializeResult()
}

// ListTools returns all tools from the server
func (tc *TestClient) ListTools(ctx context.Context) (*mcp.ListToolsResult, error) {
	return tc.Session.ListTools(ctx, &mcp.ListToolsParams{})
}

// CallTool calls a tool by name with the given arguments
func (tc *TestClient) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	return tc.Session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
}

// ListResources returns all resources from the server
func (tc *TestClient) ListResources(ctx context.Context) (*mcp.ListResourcesResult, error) {
	return tc.Session.ListResources(ctx, &mcp.ListResourcesParams{})
}

// ReadResource reads a resource by URI
func (tc *TestClient) ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error) {
	return tc.Session.ReadResource(ctx, &mcp.ReadResourceParams{
		URI: uri,
	})
}

// ListPrompts returns all prompts from the server
func (tc *TestClient) ListPrompts(ctx context.Context) (*mcp.ListPromptsResult, error) {
	return tc.Session.ListPrompts(ctx, &mcp.ListPromptsParams{})
}

// GetPrompt gets a prompt by name with arguments
func (tc *TestClient) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	return tc.Session.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      name,
		Arguments: args,
	})
}
