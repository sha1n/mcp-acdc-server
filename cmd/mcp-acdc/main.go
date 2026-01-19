package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/mark3labs/mcp-go/server"
	"github.com/sha1n/mcp-acdc-server-go/internal/config"
	"github.com/spf13/cobra"
)

var (
	// Version is injected at build time
	Version = "dev"
	// Build is injected at build time
	Build = "unknown"
	// ProgramName is injected at build time
	ProgramName = "mcp-acdc"
)

// RunParams contains dependencies for the run function
type RunParams struct {
	LoadSettings   func() (*config.Settings, error)
	ServeStdio     func(*server.MCPServer) error
	StartSSEServer func(*server.MCPServer, string) error
	CreateServer   func(*config.Settings) (*server.MCPServer, func(), error)
}

// DefaultRunParams returns production dependencies
func DefaultRunParams() RunParams {
	return RunParams{
		LoadSettings: config.LoadSettings,
		ServeStdio: func(s *server.MCPServer) error {
			return server.ServeStdio(s)
		},
		StartSSEServer: func(s *server.MCPServer, addr string) error {
			return server.NewSSEServer(s).Start(addr)
		},
		CreateServer: CreateMCPServer,
	}
}

func main() {
	rootCmd := &cobra.Command{
		Use:   ProgramName,
		Short: "MCP ACDC Server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return run()
		},
	}

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	return RunWithDeps(DefaultRunParams())
}

// RunWithDeps executes the server with the provided dependencies
func RunWithDeps(params RunParams) error {
	// Load settings
	settings, err := params.LoadSettings()
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}

	// Configure logging
	var handler slog.Handler
	if settings.Transport == "stdio" {
		// Log to stderr for stdio transport
		handler = slog.NewTextHandler(os.Stderr, nil)
	} else {
		handler = slog.NewTextHandler(os.Stdout, nil)
	}
	slog.SetDefault(slog.New(handler))

	slog.Info("Starting MCP Acdc server", "version", Version, "transport", settings.Transport)

	mcpServer, cleanup, err := params.CreateServer(settings)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	// Start server
	if settings.Transport == "stdio" {
		return params.ServeStdio(mcpServer)
	} else {
		slog.Info("Starting SSE server", "host", settings.Host, "port", settings.Port)
		return params.StartSSEServer(mcpServer, fmt.Sprintf("%s:%d", settings.Host, settings.Port))
	}
}
