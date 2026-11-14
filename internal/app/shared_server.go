package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
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

func sharedServerIdentity(flags *pflag.FlagSet) string {
	if flags == nil {
		return ""
	}
	identity, err := flags.GetString(sharedServerIdentityFlag)
	if err != nil {
		return ""
	}
	return identity
}

func runSharedServer(ctx context.Context, params RunParams, settings *config.Settings, identity, version string) error {
	if params.EndpointDir == nil {
		return errors.New("shared server requires an endpoint directory")
	}
	endpointDir, err := params.EndpointDir()
	if err != nil {
		return fmt.Errorf("failed to resolve endpoint directory: %w", err)
	}

	err = sharedserver.Run(ctx, sharedserver.Config{
		Identity:    identity,
		EndpointDir: endpointDir,
		Build: func(ctx context.Context) (*mcp.Server, func(), error) {
			components, err := params.CreateServer(ctx, settings, version)
			return components.MCP, components.Cleanup, err
		},
	})
	// Another process owns this identity: exit silently, since anything
	// written here would reach the spawning session's terminal or log.
	if errors.Is(err, sharedserver.ErrLockNotAcquired) {
		return nil
	}
	return err
}
