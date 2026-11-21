//go:build unix

package sharedserver

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type attributionStderr struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *attributionStderr) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *attributionStderr) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func requireSearchResult(t *testing.T, ctx context.Context, session *mcp.ClientSession) {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search", Arguments: map[string]any{"query": "authentication"}})
	require.NoError(t, err)
	require.False(t, result.IsError)
}

func assertRequestEntriesUnattributed(t *testing.T, stderr string) {
	t.Helper()
	assert.NotEmpty(t, logLinesContaining(stderr, `msg="Search request"`), stderr)
	assert.NotRegexp(t, anySessionAttrRe, stderr)
}

func TestSessionAttribution_PerSessionStdioLogsRequestsWithoutASession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testkit.SharedServerDeadline)
	defer cancel()
	l := testkit.StartLaunch(t, app.DefaultRunParams(), identity.Binary{Version: "test"},
		"--content-dir", newSessionProxyContentDir(t), "--transport", "stdio", "--server-mode", "session")

	session := l.Connect(ctx)
	requireSearchResult(t, ctx, session)
	require.NoError(t, session.Close())
	l.CloseStdin()
	require.NoError(t, l.Wait(testkit.SharedServerDeadline))

	assertRequestEntriesUnattributed(t, l.Stderr())
}

func TestSessionAttribution_SSELogsRequestsWithoutASession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testkit.SharedServerDeadline)
	defer cancel()
	stderr := &attributionStderr{}
	baseURL := make(chan string, 1)
	runCtx, stopRun := context.WithCancel(context.Background())
	params := app.DefaultRunParams()
	params.Stderr = stderr
	params.StartSSEServer = func(_ context.Context, logger *slog.Logger, mcpSrv *mcp.Server, settings *config.Settings, opts ...app.SSEOption) error {
		srv, err := app.NewSSEServer(logger, mcpSrv, settings, opts...)
		if err != nil {
			return err
		}
		ts := httptest.NewServer(srv.Handler)
		defer ts.Close()
		baseURL <- ts.URL
		<-runCtx.Done()
		return nil
	}
	flags := testkit.LaunchFlags(t, []string{"--content-dir", newSessionProxyContentDir(t), "--transport", "sse"})
	done := make(chan error, 1)
	go func() { done <- app.RunWithDeps(runCtx, params, flags, identity.Binary{Version: "test"}) }()
	t.Cleanup(func() {
		stopRun()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("SSE launch stopped with: %v", err)
			}
		case <-time.After(testkit.SharedServerDeadline):
			t.Error("SSE launch did not stop")
		}
	})

	var url string
	select {
	case url = <-baseURL:
	case <-ctx.Done():
		t.Fatal("SSE server did not start")
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "attribution-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.SSEClientTransport{Endpoint: url + "/sse"}, nil)
	require.NoError(t, err)
	requireSearchResult(t, ctx, session)
	require.NoError(t, session.Close())

	assertRequestEntriesUnattributed(t, stderr.String())
}
