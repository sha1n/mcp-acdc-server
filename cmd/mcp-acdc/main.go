package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/mark3labs/mcp-go/server"
	"github.com/sha1n/mcp-acdc-server-go/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
	LoadSettings   func(*pflag.FlagSet) (*config.Settings, error)
	ValidSettings  func(*config.Settings) error
	ServeStdio     func(*server.MCPServer) error
	StartSSEServer func(*server.MCPServer, *config.Settings) error
	CreateServer   func(*config.Settings) (*server.MCPServer, func(), error)
}

// DefaultRunParams returns production dependencies
func DefaultRunParams() RunParams {
	return RunParams{
		LoadSettings:  config.LoadSettingsWithFlags,
		ValidSettings: config.ValidateSettings,
		ServeStdio: func(s *server.MCPServer) error {
			return server.ServeStdio(s)
		},
		StartSSEServer: StartSSEServer,
		CreateServer:   CreateMCPServer,
	}
}

// RegisterFlags registers all CLI flags on the given FlagSet
func RegisterFlags(flags *pflag.FlagSet) {
	flags.StringP("content-dir", "c", "", "Path to content directory")
	flags.StringP("transport", "t", "", "Transport type: stdio or sse")
	flags.StringP("host", "H", "", "Host for SSE transport")
	flags.IntP("port", "p", 0, "Port for SSE transport")
	flags.IntP("search-max-results", "m", 0, "Maximum search results")
	flags.StringP("auth-type", "a", "", "Authentication type: none, basic, or apikey")
	flags.StringP("auth-basic-username", "u", "", "Basic auth username")
	flags.StringP("auth-basic-password", "P", "", "Basic auth password")
	flags.StringSliceP("auth-api-keys", "k", nil, "API keys (comma-separated)")
}

func main() {
	rootCmd := &cobra.Command{
		Use:   ProgramName,
		Short: "MCP ACDC Server",
		Long:  "Agent Content Discovery Companion (ACDC) MCP Server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithFlags(cmd.Flags())
		},
	}

	RegisterFlags(rootCmd.Flags())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runWithFlags(flags *pflag.FlagSet) error {
	return RunWithDeps(DefaultRunParams(), flags)
}

// RunWithDeps executes the server with the provided dependencies
func RunWithDeps(params RunParams, flags *pflag.FlagSet) error {
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
	// stdout may be fully-buffered when not connected to a terminal,
	// which can cause logs to not appear immediately
	handler := slog.NewTextHandler(os.Stderr, nil)
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
		return params.StartSSEServer(mcpServer, settings)
	}
}
