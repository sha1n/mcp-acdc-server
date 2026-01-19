package main

import (
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/sha1n/mcp-acdc-server-go/internal/config"
)

func TestRunWithDeps_LoadSettingsError(t *testing.T) {
	params := RunParams{
		LoadSettings: func() (*config.Settings, error) {
			return nil, errors.New("settings error")
		},
	}

	err := RunWithDeps(params)
	if err == nil {
		t.Fatal("Expected error when LoadSettings fails")
	}
	if err.Error() != "failed to load settings: settings error" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestRunWithDeps_CreateServerError(t *testing.T) {
	params := RunParams{
		LoadSettings: func() (*config.Settings, error) {
			return &config.Settings{Transport: "sse"}, nil
		},
		CreateServer: func(*config.Settings) (*server.MCPServer, func(), error) {
			return nil, nil, errors.New("create server error")
		},
	}

	err := RunWithDeps(params)
	if err == nil {
		t.Fatal("Expected error when CreateServer fails")
	}
	if err.Error() != "create server error" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestRunWithDeps_StdioTransport(t *testing.T) {
	stdioWasCalled := false
	sseWasCalled := false
	cleanupCalled := false

	params := RunParams{
		LoadSettings: func() (*config.Settings, error) {
			return &config.Settings{Transport: "stdio"}, nil
		},
		CreateServer: func(*config.Settings) (*server.MCPServer, func(), error) {
			return &server.MCPServer{}, func() { cleanupCalled = true }, nil
		},
		ServeStdio: func(*server.MCPServer) error {
			stdioWasCalled = true
			return nil
		},
		StartSSEServer: func(*server.MCPServer, string) error {
			sseWasCalled = true
			return nil
		},
	}

	err := RunWithDeps(params)
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
		LoadSettings: func() (*config.Settings, error) {
			return &config.Settings{
				Transport: "sse",
				Host:      "127.0.0.1",
				Port:      9999,
			}, nil
		},
		CreateServer: func(*config.Settings) (*server.MCPServer, func(), error) {
			return &server.MCPServer{}, func() { cleanupCalled = true }, nil
		},
		ServeStdio: func(*server.MCPServer) error {
			stdioWasCalled = true
			return nil
		},
		StartSSEServer: func(s *server.MCPServer, addr string) error {
			sseWasCalled = true
			capturedAddr = addr
			return nil
		},
	}

	err := RunWithDeps(params)
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

func TestRunWithDeps_StdioServeError(t *testing.T) {
	params := RunParams{
		LoadSettings: func() (*config.Settings, error) {
			return &config.Settings{Transport: "stdio"}, nil
		},
		CreateServer: func(*config.Settings) (*server.MCPServer, func(), error) {
			return &server.MCPServer{}, nil, nil
		},
		ServeStdio: func(*server.MCPServer) error {
			return errors.New("stdio serve error")
		},
	}

	err := RunWithDeps(params)
	if err == nil {
		t.Fatal("Expected error when ServeStdio fails")
	}
	if err.Error() != "stdio serve error" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestRunWithDeps_SSEServerError(t *testing.T) {
	params := RunParams{
		LoadSettings: func() (*config.Settings, error) {
			return &config.Settings{Transport: "sse"}, nil
		},
		CreateServer: func(*config.Settings) (*server.MCPServer, func(), error) {
			return &server.MCPServer{}, nil, nil
		},
		StartSSEServer: func(*server.MCPServer, string) error {
			return errors.New("sse start error")
		},
	}

	err := RunWithDeps(params)
	if err == nil {
		t.Fatal("Expected error when StartSSEServer fails")
	}
	if err.Error() != "sse start error" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestRunWithDeps_NilCleanup(t *testing.T) {
	// Test that nil cleanup doesn't cause a panic
	params := RunParams{
		LoadSettings: func() (*config.Settings, error) {
			return &config.Settings{Transport: "sse"}, nil
		},
		CreateServer: func(*config.Settings) (*server.MCPServer, func(), error) {
			return &server.MCPServer{}, nil, nil // nil cleanup
		},
		StartSSEServer: func(*server.MCPServer, string) error {
			return nil
		},
	}

	err := RunWithDeps(params)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

func TestDefaultRunParams(t *testing.T) {
	params := DefaultRunParams()

	if params.LoadSettings == nil {
		t.Error("LoadSettings is nil")
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
