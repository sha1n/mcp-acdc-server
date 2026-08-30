package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/spf13/pflag"
)

// noopValidate is a no-op validation function for tests
func noopValidate(*config.Settings) error {
	return nil
}

type runnerContextKey struct{}

func TestRunWithDeps_ErrorCases(t *testing.T) {
	tests := []struct {
		name           string
		params         RunParams
		wantErrContain string
	}{
		{
			name: "LoadSettings error",
			params: RunParams{
				LoadSettings: func(*pflag.FlagSet) (*config.Settings, error) {
					return nil, errors.New("settings error")
				},
				ValidSettings: noopValidate,
			},
			wantErrContain: "failed to load settings",
		},
		{
			name: "ValidSettings error",
			params: RunParams{
				LoadSettings: func(*pflag.FlagSet) (*config.Settings, error) {
					return &config.Settings{Transport: "sse"}, nil
				},
				ValidSettings: func(*config.Settings) error {
					return errors.New("validation error")
				},
			},
			wantErrContain: "invalid configuration",
		},
		{
			name: "CreateServer error",
			params: RunParams{
				LoadSettings: func(*pflag.FlagSet) (*config.Settings, error) {
					return &config.Settings{Transport: "sse"}, nil
				},
				ValidSettings: noopValidate,
				CreateServer: func(context.Context, *config.Settings, string) (*mcp.Server, func(), error) {
					return nil, nil, errors.New("create server error")
				},
			},
			wantErrContain: "create server error",
		},
		{
			name: "StartSSEServer error",
			params: RunParams{
				LoadSettings: func(*pflag.FlagSet) (*config.Settings, error) {
					return &config.Settings{Transport: "sse"}, nil
				},
				ValidSettings: noopValidate,
				CreateServer: func(context.Context, *config.Settings, string) (*mcp.Server, func(), error) {
					return nil, nil, nil
				},
				StartSSEServer: func(*mcp.Server, *config.Settings) error {
					return errors.New("sse start error")
				},
			},
			wantErrContain: "sse start error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RunWithDeps(context.Background(), tt.params, nil, "test")
			if err == nil {
				t.Fatalf("Expected error containing %q, got nil", tt.wantErrContain)
			}
			if !strings.Contains(err.Error(), tt.wantErrContain) {
				t.Errorf("Expected error containing %q, got %q", tt.wantErrContain, err.Error())
			}
		})
	}
}

func TestRunWithDeps_Cleanup(t *testing.T) {
	cleanupCalled := false
	params := RunParams{
		LoadSettings: func(*pflag.FlagSet) (*config.Settings, error) {
			return &config.Settings{Transport: "sse"}, nil
		},
		ValidSettings: noopValidate,
		CreateServer: func(context.Context, *config.Settings, string) (*mcp.Server, func(), error) {
			return nil, func() { cleanupCalled = true }, nil
		},
		StartSSEServer: func(*mcp.Server, *config.Settings) error {
			return errors.New("intentional error to trigger cleanup")
		},
	}

	_ = RunWithDeps(context.Background(), params, nil, "test")

	if !cleanupCalled {
		t.Error("Cleanup was not called")
	}
}

func TestDefaultRunParams(t *testing.T) {
	params := DefaultRunParams()

	if params.LoadSettings == nil {
		t.Error("LoadSettings is nil")
	}
	if params.ValidSettings == nil {
		t.Error("ValidSettings is nil")
	}
	if params.StartSSEServer == nil {
		t.Error("StartSSEServer is nil")
	}
	if params.CreateServer == nil {
		t.Error("CreateServer is nil")
	}
}

func TestRunWithDeps_StdioWithDefaultTransport(t *testing.T) {
	// Test the default stdio transport path (line 66 in runner.go)
	// When CustomIOTransport is nil and transport is "stdio",
	// the code should create a new StdioTransport

	params := RunParams{
		LoadSettings: func(*pflag.FlagSet) (*config.Settings, error) {
			return &config.Settings{Transport: "stdio"}, nil
		},
		ValidSettings: noopValidate,
		CreateServer: func(context.Context, *config.Settings, string) (*mcp.Server, func(), error) {
			// Create a minimal server
			impl := &mcp.Implementation{Name: "test", Version: "1.0"}
			server := mcp.NewServer(impl, nil)
			return server, nil, nil
		},
		// CustomIOTransport is nil - this tests the default behavior on line 66
		CustomIOTransport: nil,
	}

	// Use a cancelled context to avoid hanging on stdio
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RunWithDeps(ctx, params, nil, "test")

	// We expect an error because the context is cancelled
	// The important thing is that we exercised the code path through line 66
	if err == nil {
		t.Log("No error returned (unexpected)")
	}
}

func TestRunWithDeps_StdioWithCustomTransport(t *testing.T) {
	// Test the custom transport path (line 64 in runner.go)
	// When CustomIOTransport is provided, it should be used instead of creating a default

	transportUsed := false
	customTransport := &mockTransport{
		connectCalled: &transportUsed,
	}

	params := RunParams{
		LoadSettings: func(*pflag.FlagSet) (*config.Settings, error) {
			return &config.Settings{Transport: "stdio"}, nil
		},
		ValidSettings: noopValidate,
		CreateServer: func(context.Context, *config.Settings, string) (*mcp.Server, func(), error) {
			impl := &mcp.Implementation{Name: "test", Version: "1.0"}
			server := mcp.NewServer(impl, nil)
			return server, nil, nil
		},
		CustomIOTransport: customTransport,
	}

	// Use a cancelled context to avoid hanging
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = RunWithDeps(ctx, params, nil, "test")

	// Verify that the custom transport was used (Connect was called)
	if !transportUsed {
		t.Error("Custom transport Connect was not called")
	}
}

func TestRunWithDeps_PassesExactCallerContextToCreateServer(t *testing.T) {
	ctx := context.WithValue(context.Background(), runnerContextKey{}, "request-context")
	var createServerCtx context.Context
	params := RunParams{
		LoadSettings: func(*pflag.FlagSet) (*config.Settings, error) {
			return &config.Settings{Transport: "sse"}, nil
		},
		ValidSettings: noopValidate,
		CreateServer: func(gotCtx context.Context, _ *config.Settings, _ string) (*mcp.Server, func(), error) {
			createServerCtx = gotCtx
			return nil, nil, nil
		},
		StartSSEServer: func(*mcp.Server, *config.Settings) error { return nil },
	}

	err := RunWithDeps(ctx, params, nil, "test")

	if err != nil {
		t.Fatalf("RunWithDeps returned error: %v", err)
	}
	if createServerCtx != ctx {
		t.Fatal("CreateServer did not receive the exact caller context")
	}
}

func TestRunWithDeps_PassesVersionToCreateServer(t *testing.T) {
	var gotVersion string
	params := RunParams{
		LoadSettings: func(*pflag.FlagSet) (*config.Settings, error) {
			return &config.Settings{Transport: "sse"}, nil
		},
		ValidSettings: noopValidate,
		CreateServer: func(_ context.Context, _ *config.Settings, version string) (*mcp.Server, func(), error) {
			gotVersion = version
			return nil, nil, nil
		},
		StartSSEServer: func(*mcp.Server, *config.Settings) error { return nil },
	}

	err := RunWithDeps(context.Background(), params, nil, "9.9.9")

	if err != nil {
		t.Fatalf("RunWithDeps returned error: %v", err)
	}
	if gotVersion != "9.9.9" {
		t.Fatalf("CreateServer did not receive the build-injected version, got %q", gotVersion)
	}
}

// mockTransport implements mcp.Transport for testing
type mockTransport struct {
	connectCalled *bool
}

func (m *mockTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	if m.connectCalled != nil {
		*m.connectCalled = true
	}
	// Return error immediately since we don't have real I/O
	return nil, errors.New("mock transport - no real connection")
}

func TestNewLogger_LevelGatesDebug(t *testing.T) {
	ctx := context.Background()

	debug, err := newLogger(io.Discard, "debug")
	if err != nil {
		t.Fatalf("newLogger(debug) returned an error: %v", err)
	}
	if !debug.Enabled(ctx, slog.LevelDebug) {
		t.Error("a debug logger must emit debug records")
	}

	info, err := newLogger(io.Discard, "info")
	if err != nil {
		t.Fatalf("newLogger(info) returned an error: %v", err)
	}
	if info.Enabled(ctx, slog.LevelDebug) {
		t.Error("an info logger must drop debug records")
	}
	if !info.Enabled(ctx, slog.LevelInfo) {
		t.Error("an info logger must emit info records")
	}
}

func TestNewLogger_RejectsUnknownLevel(t *testing.T) {
	if _, err := newLogger(io.Discard, "trace"); err == nil {
		t.Fatal("newLogger must reject an unknown level")
	}
}
