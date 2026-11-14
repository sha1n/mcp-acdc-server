package testkit

import (
	"context"
	"fmt"
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

	resultMode := ""
	crossRef := false
	if contentOpts != nil {
		resultMode = contentOpts.ResultMode
		crossRef = contentOpts.CrossRef
	}
	return newStdioTestClientForDir(t, contentDir, resultMode, crossRef)
}

// NewStdioTestClientForDir connects a test client to an ACDC server rooted at an
// already-prepared content directory, via stdio transport. Unlike NewStdioTestClient,
// it does not call CreateTestContentDir, so the caller controls the directory's base
// name and whether .acdc/config.yaml is present. Used by zero-config tests, where the
// content root's base name drives the derived server name and no manifest must exist.
func NewStdioTestClientForDir(t testing.TB, contentDir string) *TestClient {
	t.Helper()

	return newStdioTestClientForDir(t, contentDir, "", false)
}

func newStdioTestClientForDir(t testing.TB, contentDir, resultMode string, crossRef bool) *TestClient {
	t.Helper()

	flags := NewTestFlags(t, contentDir, &FlagOptions{
		Transport:  "stdio",
		ResultMode: resultMode,
		CrossRef:   crossRef,
	})

	return connectStdioClient(t, NewTestEnv(NewACDCService("acdc-client-test", flags)))
}

func connectStdioClient(t testing.TB, env TestEnv) *TestClient {
	t.Helper()

	props, err := env.Start()
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	stdin := props["acdc.stdin"].(io.WriteCloser)
	stdout := props["acdc.stdout"].(io.ReadCloser)

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}, nil)

	transport := &mcp.IOTransport{
		Reader: stdout,
		Writer: stdin,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("%v", connectFailure(err, env.Stop()))
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

	return connectSSEClient(t, NewTestEnv(NewACDCService("acdc-sse-client-test", flags)))
}

func connectSSEClient(t testing.TB, env TestEnv) *TestClient {
	t.Helper()

	props, err := env.Start()
	if err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}, nil)

	transport := &mcp.SSEClientTransport{
		Endpoint: props["acdc.baseURL"].(string) + "/sse",
	}

	// The SSE transport uses the context for the entire connection lifecycle,
	// so we must NOT cancel it until Close() is called
	ctx, cancel := context.WithCancel(context.Background())
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		cancel()
		t.Fatalf("%v", connectFailure(err, env.Stop()))
	}

	return &TestClient{
		Session:    session,
		client:     client,
		env:        env,
		t:          t,
		cancelFunc: cancel,
	}
}

// connectFailure explains a failed client connect. When the server has already exited
// with an error, the client sees the closed transport through the SDK, which may replace
// the cause with its own closing error depending on which goroutine observes the closed
// pipe first; the server's error, collected by stopping it, is the deterministic cause.
func connectFailure(connectErr, serverErr error) error {
	if serverErr != nil {
		return fmt.Errorf("server failed: %w (client connect error: %v)", serverErr, connectErr)
	}
	return fmt.Errorf("failed to connect client: %w", connectErr)
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
		if err := tc.env.Stop(); err != nil {
			tc.t.Errorf("Failed to stop server: %v", err)
		}
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
