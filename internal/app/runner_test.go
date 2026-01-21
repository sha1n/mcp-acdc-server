package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/sha1n/mcp-acdc-server-go/internal/config"
	"github.com/spf13/pflag"
)

// noopValidate is a no-op validation function for tests
func noopValidate(*config.Settings) error {
	return nil
}

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
				CreateServer: func(*config.Settings) (*server.MCPServer, func(), error) {
					return nil, nil, errors.New("create server error")
				},
			},
			wantErrContain: "create server error",
		},
		{
			name: "ServeStdio error",
			params: RunParams{
				LoadSettings: func(*pflag.FlagSet) (*config.Settings, error) {
					return &config.Settings{Transport: "stdio"}, nil
				},
				ValidSettings: noopValidate,
				CreateServer: func(*config.Settings) (*server.MCPServer, func(), error) {
					return &server.MCPServer{}, nil, nil
				},
				ServeStdio: func(*server.MCPServer, ...server.StdioOption) error {
					return errors.New("stdio serve error")
				},
			},
			wantErrContain: "stdio serve error",
		},
		{
			name: "StartSSEServer error",
			params: RunParams{
				LoadSettings: func(*pflag.FlagSet) (*config.Settings, error) {
					return &config.Settings{Transport: "sse"}, nil
				},
				ValidSettings: noopValidate,
				CreateServer: func(*config.Settings) (*server.MCPServer, func(), error) {
					return &server.MCPServer{}, nil, nil
				},
				StartSSEServer: func(*server.MCPServer, *config.Settings) error {
					return errors.New("sse start error")
				},
			},
			wantErrContain: "sse start error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RunWithDeps(tt.params, nil, "test")
			if err == nil {
				t.Fatalf("Expected error containing %q, got nil", tt.wantErrContain)
			}
			if !strings.Contains(err.Error(), tt.wantErrContain) {
				t.Errorf("Expected error containing %q, got %q", tt.wantErrContain, err.Error())
			}
		})
	}
}

func TestRunWithDeps_StdioTransport(t *testing.T) {
	stdioWasCalled := false
	sseWasCalled := false
	cleanupCalled := false

	params := RunParams{
		LoadSettings: func(*pflag.FlagSet) (*config.Settings, error) {
			return &config.Settings{Transport: "stdio"}, nil
		},
		ValidSettings: noopValidate,
		CreateServer: func(*config.Settings) (*server.MCPServer, func(), error) {
			return &server.MCPServer{}, func() { cleanupCalled = true }, nil
		},
		ServeStdio: func(*server.MCPServer, ...server.StdioOption) error {
			stdioWasCalled = true
			return nil
		},
		StartSSEServer: func(*server.MCPServer, *config.Settings) error {
			sseWasCalled = true
			return nil
		},
	}

	err := RunWithDeps(params, nil, "test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if !stdioWasCalled {
		t.Error("ServeStdio was not called")
	}
	if sseWasCalled {
		t.Error("StartSSEServer was unexpectedly called")
	}
	if !cleanupCalled {
		t.Error("Cleanup was not called")
	}
}

func TestRunWithDeps_SSETransport(t *testing.T) {
	stdioWasCalled := false
	sseWasCalled := false
	cleanupCalled := false
	capturedAddr := ""

	params := RunParams{
		LoadSettings: func(*pflag.FlagSet) (*config.Settings, error) {
			return &config.Settings{
				Transport: "sse",
				Host:      "127.0.0.1",
				Port:      9999,
			}, nil
		},
		ValidSettings: noopValidate,
		CreateServer: func(*config.Settings) (*server.MCPServer, func(), error) {
			return &server.MCPServer{}, func() { cleanupCalled = true }, nil
		},
		ServeStdio: func(*server.MCPServer, ...server.StdioOption) error {
			stdioWasCalled = true
			return nil
		},
		StartSSEServer: func(s *server.MCPServer, settings *config.Settings) error {
			sseWasCalled = true
			capturedAddr = fmt.Sprintf("%s:%d", settings.Host, settings.Port)
			return nil
		},
	}

	err := RunWithDeps(params, nil, "test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if stdioWasCalled {
		t.Error("ServeStdio was unexpectedly called")
	}
	if !sseWasCalled {
		t.Error("StartSSEServer was not called")
	}
	if capturedAddr != "127.0.0.1:9999" {
		t.Errorf("Unexpected address: %s", capturedAddr)
	}
	if !cleanupCalled {
		t.Error("Cleanup was not called")
	}
}

func TestRunWithDeps_NilCleanup(t *testing.T) {
	// Test that nil cleanup doesn't cause a panic
	params := RunParams{
		LoadSettings: func(*pflag.FlagSet) (*config.Settings, error) {
			return &config.Settings{Transport: "sse"}, nil
		},
		ValidSettings: noopValidate,
		CreateServer: func(*config.Settings) (*server.MCPServer, func(), error) {
			return &server.MCPServer{}, nil, nil // nil cleanup
		},
		StartSSEServer: func(*server.MCPServer, *config.Settings) error {
			return nil
		},
	}

	err := RunWithDeps(params, nil, "test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
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
	if params.ServeStdio == nil {
		t.Error("ServeStdio is nil")
	}
	if params.StartSSEServer == nil {
		t.Error("StartSSEServer is nil")
	}
	if params.CreateServer == nil {
		t.Error("CreateServer is nil")
	}
}
