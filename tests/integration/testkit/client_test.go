package testkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStdioTestClient_ListResources(t *testing.T) {
	client := NewStdioTestClient(t, &ContentDirOptions{
		Resources: map[string]string{
			"test-resource.md": "---\nname: Test Resource\ndescription: A test resource\n---\nTest content",
		},
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := client.ListResources(ctx)
	if err != nil {
		t.Fatalf("ListResources failed: %v", err)
	}

	if len(result.Resources) == 0 {
		t.Error("Expected at least one resource")
	}

	found := false
	for _, r := range result.Resources {
		if r.Name == "Test Resource" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected to find 'Test Resource' in resources list")
	}
}

func TestSSETestClient_ListResources(t *testing.T) {
	client := NewSSETestClient(t, &ContentDirOptions{
		Resources: map[string]string{
			"sse-resource.md": "---\nname: SSE Resource\ndescription: A test resource via SSE\n---\nSSE content",
		},
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := client.ListResources(ctx)
	if err != nil {
		t.Fatalf("ListResources failed: %v", err)
	}

	if len(result.Resources) == 0 {
		t.Error("Expected at least one resource")
	}

	found := false
	for _, r := range result.Resources {
		if r.Name == "SSE Resource" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected to find 'SSE Resource' in resources list")
	}
}

func TestStdioTestClient_NilContentOpts(t *testing.T) {
	client := NewStdioTestClient(t, nil)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Verify initialization worked
	initResult := client.InitializeResult()
	if initResult == nil {
		t.Error("Expected initialize result")
	}

	// Verify ListTools works with default content
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Error("Expected at least one tool")
	}
}

func TestSSETestClient_NilContentOpts(t *testing.T) {
	client := NewSSETestClient(t, nil)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Verify initialization worked
	initResult := client.InitializeResult()
	if initResult == nil {
		t.Error("Expected initialize result")
	}

	// Verify ListTools works with default content
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Error("Expected at least one tool")
	}
}

// fatalPanic is used to simulate t.Fatalf() stopping execution
type fatalPanic struct {
	msg string
}

// clientMockTB implements testing.TB for testing error paths
type clientMockTB struct {
	testing.TB
	failed   bool
	fatalMsg string
	tempDir  string
}

func (m *clientMockTB) Fatalf(format string, args ...any) {
	m.failed = true
	m.fatalMsg = fmt.Sprintf(format, args...)
	// Panic to stop execution like real Fatalf does
	panic(fatalPanic{msg: m.fatalMsg})
}

func (m *clientMockTB) Helper() {}

func (m *clientMockTB) TempDir() string {
	return m.tempDir
}

func TestNewStdioTestClient_StartError(t *testing.T) {
	mockT := &clientMockTB{TB: t, tempDir: t.TempDir()}
	missingContentDir := filepath.Join(t.TempDir(), "missing")

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(fatalPanic); !ok {
					panic(r)
				}
			}
		}()
		NewStdioTestClientForDir(mockT, missingContentDir)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Expected the startup failure to be reported without waiting for the connect deadline")
	}

	if !mockT.failed {
		t.Fatal("Expected NewStdioTestClientForDir to fail for a missing content directory")
	}
	if !strings.Contains(mockT.fatalMsg, "not found or not a directory") {
		t.Errorf("Expected the reported failure to name the startup error, got %q", mockT.fatalMsg)
	}
}

func TestNewSSETestClient_StartError(t *testing.T) {
	mockT := &clientMockTB{TB: t, tempDir: t.TempDir()}

	// Recover from the panic that mockT.Fatalf() causes
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(fatalPanic); ok {
				// Expected - the error path was triggered and called Fatalf
				if !mockT.failed {
					t.Error("Expected mockT.failed to be true")
				}
			} else {
				// Re-panic if it's not our expected panic
				panic(r)
			}
		}
	}()

	// Use metadata that is valid YAML but fails validation (missing required fields)
	// This will cause CreateServer to fail, which triggers the Start error path
	// Note: This only works for SSE which polls and detects startup errors
	NewSSETestClient(mockT, &ContentDirOptions{
		Metadata: "server: { name: \"\", version: \"\", instructions: \"\" }",
	})

	// If we reach here, the error path wasn't triggered
	t.Error("Expected NewSSETestClient to fail due to invalid metadata")
}

func TestConnectFailure_ServerErrorObservedAfterConnectError(t *testing.T) {
	startupErr := errors.New("content directory /x/missing not found or not a directory")
	connectErr := errors.New(`connection closed: calling "initialize": client is closing: EOF`)

	got := connectFailure(connectErr, startupErr)

	if !strings.Contains(got.Error(), startupErr.Error()) {
		t.Errorf("Expected the failure to name the server's startup error, got %q", got)
	}
	if !strings.Contains(got.Error(), connectErr.Error()) {
		t.Errorf("Expected the failure to keep the connect error for diagnosis, got %q", got)
	}
}

func TestConnectFailure_ServerStoppedCleanly(t *testing.T) {
	connectErr := errors.New("dial tcp: connection refused")

	got := connectFailure(connectErr, nil)

	if !errors.Is(got, connectErr) {
		t.Errorf("Expected the connect error to be reported, got %q", got)
	}
}

// errStartupCause is only ever returned by the fake service's Stop, the way acdcService
// publishes its runner error only to Stop once the client has already failed to connect.
var errStartupCause = errors.New("content directory /x/missing not found or not a directory")

func TestConnectStdioClient_ConnectFailsBeforeTheStartupErrorIsAvailable(t *testing.T) {
	transportClosed := errors.New("transport closed by the exited server")
	env := NewTestEnv(&mockService{
		name: "stdio-startup-failure",
		startFunc: func() (map[string]any, error) {
			stdinReader, stdinWriter := io.Pipe()
			_ = stdinReader.CloseWithError(transportClosed)
			stdoutReader, stdoutWriter := io.Pipe()
			_ = stdoutWriter.CloseWithError(transportClosed)
			return map[string]any{"acdc.stdin": stdinWriter, "acdc.stdout": stdoutReader}, nil
		},
		stopFunc: func() error { return errStartupCause },
	})

	msg := fatalMessageOf(t, func(tb testing.TB) { connectStdioClient(tb, env) })

	if !strings.Contains(msg, errStartupCause.Error()) {
		t.Errorf("Expected the reported failure to name the startup error, got %q", msg)
	}
}

func TestConnectSSEClient_ConnectFailsBeforeTheStartupErrorIsAvailable(t *testing.T) {
	exitedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "server exited", http.StatusServiceUnavailable)
	}))
	t.Cleanup(exitedServer.Close)
	env := NewTestEnv(&mockService{
		name: "sse-startup-failure",
		startFunc: func() (map[string]any, error) {
			return map[string]any{"acdc.baseURL": exitedServer.URL}, nil
		},
		stopFunc: func() error { return errStartupCause },
	})

	msg := fatalMessageOf(t, func(tb testing.TB) { connectSSEClient(tb, env) })

	if !strings.Contains(msg, errStartupCause.Error()) {
		t.Errorf("Expected the reported failure to name the startup error, got %q", msg)
	}
}

func fatalMessageOf(t *testing.T, construct func(testing.TB)) string {
	t.Helper()
	mockT := &clientMockTB{TB: t, tempDir: t.TempDir()}
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(fatalPanic); !ok {
					panic(r)
				}
			}
		}()
		construct(mockT)
	}()
	if !mockT.failed {
		t.Fatal("Expected the client constructor to fail")
	}
	return mockT.fatalMsg
}

func TestConnectFailure_OmitsTheMissingSide(t *testing.T) {
	serverErr := errors.New("server exited")
	connectErr := errors.New("connection refused")

	tests := []struct {
		name       string
		connectErr error
		serverErr  error
		wantIs     error
	}{
		{name: "server error only", serverErr: serverErr, wantIs: serverErr},
		{name: "connect error only", connectErr: connectErr, wantIs: connectErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := connectFailure(tt.connectErr, tt.serverErr)

			if !errors.Is(got, tt.wantIs) {
				t.Fatalf("Expected %q to wrap %q", got, tt.wantIs)
			}
			if strings.Contains(got.Error(), "nil") {
				t.Errorf("Expected no placeholder for the missing error, got %q", got)
			}
		})
	}
}

func TestConnectFailure_NoErrors(t *testing.T) {
	if got := connectFailure(nil, nil); got != nil {
		t.Errorf("Expected no failure when neither side failed, got %q", got)
	}
}
