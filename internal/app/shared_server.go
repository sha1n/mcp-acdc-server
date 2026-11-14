package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/spf13/pflag"
)

// sharedServerIdentityFlag marks a re-exec of the binary as a shared server.
// It is read from the flag set only, never through viper, so no ACDC_MCP_*
// variable or config key can set it and the marker cannot be inherited
// through the environment.
const sharedServerIdentityFlag = "shared-server-identity"

func registerSharedServerFlag(flags *pflag.FlagSet) {
	flags.String(sharedServerIdentityFlag, "", "Internal: run as the shared server for this server identity")
	_ = flags.MarkHidden(sharedServerIdentityFlag)
}

// sharedServerIdentity reports whether the marker was supplied at all, so an
// explicit empty or malformed value is rejected instead of falling through to
// an ordinary launch.
func sharedServerIdentity(flags *pflag.FlagSet) (identity string, supplied bool) {
	if flags == nil {
		return "", false
	}
	flag := flags.Lookup(sharedServerIdentityFlag)
	if flag == nil || !flag.Changed {
		return "", false
	}
	return flag.Value.String(), true
}

func runSharedServer(ctx context.Context, params RunParams, settings *config.Settings, identity, version string) error {
	if !endpoint.ValidIdentity(identity) {
		return fmt.Errorf("invalid shared server identity %q", identity)
	}
	if params.EndpointDir == nil {
		return errors.New("shared server requires an endpoint directory")
	}
	endpointDir, err := params.EndpointDir()
	if err != nil {
		return fmt.Errorf("failed to resolve endpoint directory: %w", err)
	}

	var revalidate func(context.Context)
	err = sharedserver.Run(ctx, sharedserver.Config{
		Identity:    identity,
		EndpointDir: endpointDir,
		LogDir:      sharedServerLogDir(params),
		LogStartup: func(logger *slog.Logger) {
			logSharedServerStartup(logger, settings, version)
		},
		Build: func(ctx context.Context) (*mcp.Server, func(), error) {
			components, err := params.CreateServer(ctx, settings, version)
			revalidate = components.RevalidateNow
			return components.MCP, components.Cleanup, err
		},
		Revalidate: func(ctx context.Context) { revalidate(ctx) },
	})
	// Another process owns this identity: exit silently, since anything
	// written here would reach the spawning session's terminal or log.
	if errors.Is(err, sharedserver.ErrLockNotAcquired) {
		return nil
	}
	return err
}
