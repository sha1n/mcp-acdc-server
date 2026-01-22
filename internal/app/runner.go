package app

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/mark3labs/mcp-go/server"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/spf13/pflag"
)

// RunParams contains dependencies for the run function
type RunParams struct {
	LoadSettings   func(*pflag.FlagSet) (*config.Settings, error)
	ValidSettings  func(*config.Settings) error
	ServeStdio     func(*server.MCPServer, ...server.StdioOption) error
	StartSSEServer func(*server.MCPServer, *config.Settings) error
	CreateServer   func(*config.Settings) (*server.MCPServer, func(), error)
}

// DefaultRunParams returns production dependencies
func DefaultRunParams() RunParams {
	return RunParams{
		LoadSettings:   config.LoadSettingsWithFlags,
		ValidSettings:  config.ValidateSettings,
		ServeStdio:     server.ServeStdio,
		StartSSEServer: StartSSEServer,
		CreateServer:   CreateMCPServer,
	}
}

// RunWithDeps executes the server with the provided dependencies
func RunWithDeps(params RunParams, flags *pflag.FlagSet, version string) error {
	// Load settings
	settings, err := params.LoadSettings(flags)
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}

	// Validate settings for conflicting configurations
	if err := params.ValidSettings(settings); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Configure logging - always use stderr to avoid buffering issues
	handler := slog.NewTextHandler(os.Stderr, nil)
	slog.SetDefault(slog.New(handler))

	slog.Info("Starting MCP Acdc server", "version", version)
	config.Log(settings)

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
		return params.StartSSEServer(mcpServer, settings)
	}
}
