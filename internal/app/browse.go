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
	noOpenFlag:               true,
}

// BrowseRunParams returns the production dependencies of browse: a shared
// server it spawns is launched with the settings flags of the browse
// command, not with its command line.
func BrowseRunParams(flags *pflag.FlagSet) RunParams {
	params := DefaultRunParams()
	params.SpawnSharedServer = spawnSharedServerWith(BrowseSharedServerArgs(flags))
	params.OpenBrowser = browserOpener(runtime.GOOS, browserOpenerWait)
	return params
}

// BrowseSharedServerArgs returns the flags set on a browse command as the
// arguments of a shared server launch, without the ignored ones and
// --no-open.
func BrowseSharedServerArgs(flags *pflag.FlagSet) []string {
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
// it on stdout, then opens the system browser on it unless --no-open is
// set. A browser that cannot be opened is a warning, not a failure. A
// requested UI is a required UI: when no URL can be had it fails.
//
// Without a shared server (session server mode, or Windows) it builds the
// catalog itself and serves the Web UI in the foreground on a loopback
// ephemeral port until ctx is cancelled or the process is interrupted, then
// returns nil.
func RunBrowse(ctx context.Context, params RunParams, flags *pflag.FlagSet, binary identity.Binary) error {
	settings, err := params.LoadSettings(flags)
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}
	if err := validateBrowseSettings(params.ValidSettings, settings); err != nil {
		return err
	}
	if params.Stderr == nil || params.Stdout == nil {
		return errors.New("browse requires stderr and stdout")
	}
	logger := slog.New(slog.NewTextHandler(params.Stderr, nil))
	if effectiveServerMode(logger, runtime.GOOS, settings.ServerMode) != config.ServerModeShared {
		return runForegroundBrowse(ctx, params, logger, settings, flags, binary)
	}
	if params.EndpointDir == nil || params.SpawnSharedServer == nil {
		return errors.New("browse requires an endpoint directory and a shared server spawn")
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
	if _, err := fmt.Fprintln(params.Stdout, ready.UIURL); err != nil {
		return err
	}
	if !noOpen(flags) {
		openBrowser(logger, params.OpenBrowser, ready.UIURL)
	}
	return nil
}

// noOpen reports whether --no-open is set on flags.
func noOpen(flags *pflag.FlagSet) bool {
	if flags == nil || flags.Lookup(noOpenFlag) == nil {
		return false
	}
	set, err := flags.GetBool(noOpenFlag)
	return err == nil && set
}

// openBrowser opens the browser on url with open, and only warns when it
// cannot: the URL is already printed.
func openBrowser(logger *slog.Logger, open func(string) error, url string) {
	err := errors.New("no browser opener")
	if open != nil {
		err = open(url)
	}
	switch {
	case errors.Is(err, errBrowserOpenUnconfirmed):
		logger.Warn("Could not confirm the browser opened; open the Web UI URL manually if it did not", "url", url, "error", err)
	case err != nil:
		logger.Warn("Failed to open the browser; open the Web UI URL manually", "url", url, "error", err)
	default:
		logger.Info("Opened the browser", "url", url)
	}
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
