package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/spf13/pflag"
)

// RunParams contains dependencies for the run function
type RunParams struct {
	LoadSettings      func(*pflag.FlagSet) (*config.Settings, error)
	ValidSettings     func(*config.Settings) error
	StartSSEServer    func(*mcp.Server, *config.Settings) error
	CreateServer      func(context.Context, *config.Settings, string) (*mcp.Server, func(), error)
	CustomIOTransport mcp.Transport // Optional: for testing with custom IO
}

// DefaultRunParams returns production dependencies
func DefaultRunParams() RunParams {
	return RunParams{
		LoadSettings:   config.LoadSettingsWithFlags,
		ValidSettings:  config.ValidateSettings,
		StartSSEServer: StartSSEServer,
		// Wrapped rather than assigned directly: CreateMCPServer is
		// variadic in its options, which RunParams.CreateServer is not.
		CreateServer: func(ctx context.Context, settings *config.Settings, version string) (*mcp.Server, func(), error) {
			return CreateMCPServer(ctx, settings, version)
		},
	}
}

// RunWithDeps executes the server with the provided dependencies
func RunWithDeps(ctx context.Context, params RunParams, flags *pflag.FlagSet, version string) error {
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
	logger, err := newLogger(os.Stderr, settings.LogLevel)
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	slog.SetDefault(logger)

	slog.Info("Starting MCP Acdc server", "version", version)
	config.Log(settings)

	mcpServer, cleanup, err := params.CreateServer(ctx, settings, version)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	// Start server
	if settings.Transport == "stdio" {
		// Use custom transport if provided (for testing), otherwise use stdio
		transport := params.CustomIOTransport
		if transport == nil {
			transport = &mcp.StdioTransport{}
		}
		return mcpServer.Run(ctx, transport)
	} else {
		slog.Info("Starting SSE server", "host", settings.Host, "port", settings.Port)
		return params.StartSSEServer(mcpServer, settings)
	}
}

// newLogger builds the process logger at the configured severity. Levels are
// resolved through config.ParseLogLevel so the runner and settings validation
// accept exactly the same names.
func newLogger(out io.Writer, level string) (*slog.Logger, error) {
	parsed, err := config.ParseLogLevel(level)
	if err != nil {
		return nil, err
	}

	return slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: parsed})), nil
}
