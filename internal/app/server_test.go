package app

import (
	"net"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/sha1n/mcp-acdc-server-go/internal/config"
)

func TestNewSSEServer(t *testing.T) {
	tests := []struct {
		name     string
		settings *config.Settings
		wantErr  bool
		wantAddr string
	}{
		{
			name: "no auth",
			settings: &config.Settings{
				Host: "localhost",
				Port: 0,
				Auth: config.AuthSettings{Type: config.AuthTypeNone},
			},
			wantErr:  false,
			wantAddr: "localhost:0",
		},
		{
			name: "API key auth",
			settings: &config.Settings{
				Host: "localhost",
				Port: 0,
				Auth: config.AuthSettings{
					Type:    config.AuthTypeAPIKey,
					APIKeys: []string{"test-key"},
				},
			},
			wantErr: false,
		},
		{
			name: "basic auth",
			settings: &config.Settings{
				Host: "localhost",
				Port: 0,
				Auth: config.AuthSettings{
					Type: config.AuthTypeBasic,
					Basic: config.BasicAuthSettings{
						Username: "user",
						Password: "password",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid auth type",
			settings: &config.Settings{
				Host: "localhost",
				Port: 0,
				Auth: config.AuthSettings{Type: "invalid"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mcpSrv := server.NewMCPServer("test", "1.0")
			srv, err := NewSSEServer(mcpSrv, tt.settings)

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("NewSSEServer failed: %v", err)
			}
			if srv == nil {
				t.Fatal("Expected non-nil server")
			}
			if tt.wantAddr != "" && srv.Addr != tt.wantAddr {
				t.Errorf("Expected addr %s, got %s", tt.wantAddr, srv.Addr)
			}
		})
	}
}

func TestStartSSEServer_NewSSEServerError(t *testing.T) {
	mcpSrv := server.NewMCPServer("test", "1.0")
	settings := &config.Settings{
		Auth: config.AuthSettings{Type: "invalid"},
	}
	err := StartSSEServer(mcpSrv, settings)
	if err == nil {
		t.Error("Expected error for invalid auth type")
	}
}

func TestStartSSEServer_PortCollision(t *testing.T) {
	mcpSrv := server.NewMCPServer("test", "1.0")

	// Bind to a port
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Skip("Failed to bind to local port for test")
	}
	defer func() { _ = l.Close() }()
	port := l.Addr().(*net.TCPAddr).Port

	settings := &config.Settings{
		Host: "localhost",
		Port: port,
		Auth: config.AuthSettings{Type: config.AuthTypeNone},
	}

	err = StartSSEServer(mcpSrv, settings)
	if err == nil {
		t.Error("Expected error because port is already in use")
	}
}
