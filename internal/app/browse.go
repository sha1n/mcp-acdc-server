package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/spf13/pflag"
)

// browseIgnoredFlags are accepted by browse and have no effect on it; a
// shared server spawned by browse does not get them either.
var browseIgnoredFlags = map[string]bool{
	"host":                   true,
	"port":                   true,
	"ui-enabled":             true,
	"auth-type":              true,
	"auth-basic-username":    true,
	"auth-basic-password":    true,
	"auth-api-keys":          true,
	sharedServerIdentityFlag: true,
}

// BrowseRunParams returns the production dependencies of browse: a shared
// server it spawns is launched with the settings flags of the browse
// command, not with its command line.
func BrowseRunParams(flags *pflag.FlagSet) RunParams {
	params := DefaultRunParams()
	params.SpawnSharedServer = spawnSharedServerWith(browseSharedServerArgs(flags))
	return params
}

// browseSharedServerArgs returns the flags set on a browse command as the
// arguments of a shared server launch, without the ignored ones.
func browseSharedServerArgs(flags *pflag.FlagSet) []string {
	var args []string
	flags.Visit(func(f *pflag.Flag) {
		if browseIgnoredFlags[f.Name] {
			return
		}
		if slice, ok := f.Value.(pflag.SliceValue); ok {
			for _, item := range slice.GetSlice() {
				args = append(args, "--"+f.Name+"="+item)
			}
			return
		}
		args = append(args, "--"+f.Name+"="+f.Value.String())
	})
	return args
}

// RunBrowse finds or starts the shared server for the identity of the
// settings, as a session proxy does, gets the URL of its Web UI and prints
// it on stdout. A requested UI is a required UI: when no URL can be had it
// fails, and it never serves anything itself.
func RunBrowse(ctx context.Context, params RunParams, flags *pflag.FlagSet, binary identity.Binary) error {
	settings, err := params.LoadSettings(flags)
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}
	if err := validateBrowseSettings(params.ValidSettings, settings); err != nil {
		return err
	}
	if params.Stderr == nil || params.Stdout == nil || params.EndpointDir == nil || params.SpawnSharedServer == nil {
		return errors.New("browse requires stderr, stdout, an endpoint directory and a shared server spawn")
	}
	logger := slog.New(slog.NewTextHandler(params.Stderr, nil))
	if effectiveServerMode(logger, runtime.GOOS, settings.ServerMode) != config.ServerModeShared {
		return errors.New("browse requires a shared server: set server_mode to shared, on a Unix platform")
	}

	ready, err := sessionproxy.Browse(ctx, sessionproxy.BrowseConfig{
		EndpointDir:  params.EndpointDir,
		Identity:     func() (string, error) { return identity.Compute(settings, binary) },
		ConnectWait:  settings.SharedServer.ConnectWait,
		Spawn:        params.SpawnSharedServer,
		UIMaxResults: settings.UI.MaxResults,
		Logger:       logger,
	})
	if err != nil {
		if ready.Log != "" {
			return fmt.Errorf("failed to get the Web UI URL: %w (shared server log: %s)", err, ready.Log)
		}
		return fmt.Errorf("failed to get the Web UI URL: %w", err)
	}
	logger.Info("Web UI ready", "url", ready.UIURL, "log", ready.Log)
	_, err = fmt.Fprintln(params.Stdout, ready.UIURL)
	return err
}

// validateBrowseSettings validates the settings browse uses: authentication,
// host and port are ignored, and ui.max_results is checked whether or not
// ui.enabled is set.
func validateBrowseSettings(validate func(*config.Settings) error, settings *config.Settings) error {
	checked := withoutAuth(settings)
	checked.UI.Enabled = true
	if err := validate(checked); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	if settings.Transport != "stdio" {
		return fmt.Errorf("browse does not support the %s transport", settings.Transport)
	}
	return nil
}
