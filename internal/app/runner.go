package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"github.com/spf13/pflag"
)

// RunParams contains dependencies for the run function
type RunParams struct {
	LoadSettings      func(*pflag.FlagSet) (*config.Settings, error)
	ValidSettings     func(*config.Settings) error
	StartSSEServer    func(*slog.Logger, *mcp.Server, *config.Settings, ...SSEOption) error
	CreateServer      func(context.Context, *slog.Logger, *config.Settings, string) (Components, error)
	CustomIOTransport mcp.Transport // Optional: for testing with custom IO
	EndpointDir       func() (string, error)
	LogDir            func() (string, error)
	Stderr            io.Writer
	// SpawnSharedServer starts a candidate shared server for an identity
	// without waiting for it.
	SpawnSharedServer func(identity string) error
	Stdin             io.Reader
	Stdout            io.Writer
}

// DefaultRunParams returns production dependencies
func DefaultRunParams() RunParams {
	return RunParams{
		LoadSettings:      config.LoadSettingsWithFlags,
		ValidSettings:     config.ValidateSettings,
		StartSSEServer:    StartSSEServer,
		CreateServer:      CreateServer,
		EndpointDir:       sharedserver.DefaultEndpointDir,
		LogDir:            sharedserver.DefaultLogDir,
		Stderr:            os.Stderr,
		SpawnSharedServer: spawnSharedServer,
		Stdin:             os.Stdin,
		Stdout:            os.Stdout,
	}
}

// RunWithDeps executes the server with the provided dependencies
func RunWithDeps(ctx context.Context, params RunParams, flags *pflag.FlagSet, binary identity.Binary) error {
	// Load settings
	settings, err := params.LoadSettings(flags)
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}

	// Validate settings for conflicting configurations
	if err := params.ValidSettings(settings); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	if serverIdentity, supplied := sharedServerIdentity(flags); supplied {
		return runSharedServer(ctx, params, settings, serverIdentity, binary)
	}

	if params.Stderr == nil {
		return errors.New("runner requires a stderr writer")
	}
	// Always log to stderr: stdout carries the stdio protocol stream.
	logger := slog.New(slog.NewTextHandler(params.Stderr, nil))

	logger.Info("Starting MCP Acdc server", "version", binary.Version)
	config.LogWithLogger(settings, logger)

	if attemptsDiscovery(logger, runtime.GOOS, settings) {
		return runSessionProxy(ctx, logger, params, settings, binary)
	}

	if settings.Transport == "stdio" {
		// Use custom transport if provided (for testing), otherwise use stdio
		transport := params.CustomIOTransport
		if transport == nil {
			transport = &mcp.StdioTransport{}
		}
		return runPerSession(ctx, logger, params, settings, binary.Version, transport)
	}

	components, err := params.CreateServer(ctx, logger, settings, binary.Version)
	if err != nil {
		return err
	}
	if components.Cleanup != nil {
		defer components.Cleanup()
	}
	logger.Info("Starting SSE server", "host", settings.Host, "port", settings.Port)
	return params.StartSSEServer(logger, components.MCP, settings, components.SSEOptions...)
}
