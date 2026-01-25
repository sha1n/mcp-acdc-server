package testkit

import (
	"context"
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
	m.fatalMsg = format
	// Panic to stop execution like real Fatalf does
	panic(fatalPanic{msg: format})
}

func (m *clientMockTB) Helper() {}

func (m *clientMockTB) TempDir() string {
	return m.tempDir
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
	// This will cause CreateMCPServer to fail, which triggers the Start error path
	// Note: This only works for SSE which polls and detects startup errors
	NewSSETestClient(mockT, &ContentDirOptions{
		Metadata: "server: { name: \"\", version: \"\", instructions: \"\" }",
	})

	// If we reach here, the error path wasn't triggered
	t.Error("Expected NewSSETestClient to fail due to invalid metadata")
}
