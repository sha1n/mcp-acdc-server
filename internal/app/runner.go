package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/webui"
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
	// The web UI backend is the search service built by CreateServer, so the
	// two closures share it. RunWithDeps always calls CreateServer before
	// StartSSEServer, which is the ordering this relies on; it keeps the
	// RunParams function types unchanged.
	var uiBackend webui.Backend
	return RunParams{
		LoadSettings:  config.LoadSettingsWithFlags,
		ValidSettings: config.ValidateSettings,
		StartSSEServer: func(s *mcp.Server, settings *config.Settings) error {
			return StartSSEServer(s, settings, WithUIBackend(uiBackend))
		},
		CreateServer: func(ctx context.Context, settings *config.Settings, version string) (*mcp.Server, func(), error) {
			server, backend, cleanup, err := createServer(ctx, settings, version, defaultFactoryDeps())
			uiBackend = backend
			return server, cleanup, err
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
	handler := slog.NewTextHandler(os.Stderr, nil)
	slog.SetDefault(slog.New(handler))

	slog.Info("Starting MCP Acdc server", "version", version)
	config.Log(settings)
	if settings.Transport == "stdio" && settings.UI.Enabled {
		slog.Warn("Web UI requires the sse transport; ignoring ui.enabled", "transport", settings.Transport)
	}

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
