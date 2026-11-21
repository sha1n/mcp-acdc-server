package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
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

// withoutAuth returns a copy of settings without authentication. A shared
// server serves stdio sessions and a loopback Web UI only, so authentication
// never applies to it, and settings it inherits from the environment or the
// config file of its spawner, such as browse, which ignores them, must not
// stop it.
func withoutAuth(settings *config.Settings) *config.Settings {
	checked := *settings
	checked.Auth = config.AuthSettings{Type: config.AuthTypeNone}
	return &checked
}

func runSharedServer(ctx context.Context, params RunParams, settings *config.Settings, serverIdentity string, binary identity.Binary) error {
	if !endpoint.ValidIdentity(serverIdentity) {
		return fmt.Errorf("invalid shared server identity %q", serverIdentity)
	}
	if params.EndpointDir == nil {
		return errors.New("shared server requires an endpoint directory")
	}
	endpointDir, err := params.EndpointDir()
	if err != nil {
		return fmt.Errorf("failed to resolve endpoint directory: %w", err)
	}

	// Exit is immediate on a signal: attached connections simply drop and
	// their session proxies recover as from a crash.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, os.Interrupt)
	defer stop()
	// Run may still wait on a handler or a build that ignores cancellation;
	// a second signal must then terminate the process with the default
	// disposition.
	context.AfterFunc(ctx, stop)

	built := &sharedServerComponents{create: params.CreateServer, settings: settings, version: binary.Version}
	err = sharedserver.Run(ctx, sharedserver.Config{
		Identity:    serverIdentity,
		EndpointDir: endpointDir,
		LogDir:      sharedServerLogDir(params),
		LogStartup: func(logger *slog.Logger) {
			logSharedServerStartup(logger, settings, binary.Version)
		},
		CurrentIdentity: func() (string, error) { return identity.Compute(settings, binary) },
		Linger:          settings.SharedServer.Linger,
		Build:           built.build,
		Revalidate:      built.revalidate,
		UIHandler:       built.uiHandler,
		UIListen:        params.ListenWebUI,
		ViewerWindow:    params.ViewerWindow,
	})
	// Another process owns this identity: exit silently, since anything
	// written here would reach the spawning session's terminal or log.
	if errors.Is(err, sharedserver.ErrLockNotAcquired) {
		return nil
	}
	return err
}

// sharedServerComponents keeps what Build produced for the hooks the shared
// server calls only after Build has returned.
type sharedServerComponents struct {
	create     func(context.Context, *slog.Logger, *config.Settings, string) (Components, error)
	settings   *config.Settings
	version    string
	logger     *slog.Logger
	components Components
}

func (c *sharedServerComponents) build(ctx context.Context, logger *slog.Logger) (*mcp.Server, func(), error) {
	components, err := c.create(ctx, logger, c.settings, c.version)
	c.logger = logger
	c.components = components
	return components.MCP, components.Cleanup, err
}

func (c *sharedServerComponents) revalidate(ctx context.Context) {
	c.components.RevalidateNow(ctx)
}

// uiHandler serves the Web UI of a shared server from the built components,
// reporting its viewer heartbeats to heartbeat.
func (c *sharedServerComponents) uiHandler(maxResults int, heartbeat func(viewerID string)) (http.Handler, error) {
	return newWebUIHandler(c.logger, c.components.SSEOptions, maxResults, heartbeat)
}
