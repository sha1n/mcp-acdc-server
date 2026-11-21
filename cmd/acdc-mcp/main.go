package main

import (
	"context"
	"os"

	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	// Version is injected at build time
	Version = "dev"
	// Build is injected at build time
	Build = "unknown"
	// ProgramName is injected at build time
	ProgramName = "acdc-mcp"
)

func main() {
	runMain(os.Args, os.Exit)
}

func runMain(args []string, exit func(int)) {
	if err := Execute(Version, Build, ProgramName, args[1:]); err != nil {
		exit(1)
	}
}

// Execute is the entry point for the CLI, extracted for testing
func Execute(version, build, programName string, args []string) error {
	rootCmd := &cobra.Command{
		Use:     programName,
		Short:   "ACDC MCP Server",
		Long:    "Agent Content Discovery Companion (ACDC) MCP Server",
		Version: version,
		// A startup error (e.g. the legacy-layout refusal) is the operator's
		// entire actionable message; cobra's default flag-usage dump after it
		// buries that message under ~25 lines of help text.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithFlags(cmd.Flags(), binary(version, build))
		},
	}

	rootCmd.SetVersionTemplate(`{{.Version}}
`)

	// Only the commands this program documents: no shell completion command.
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	app.RegisterFlags(rootCmd.Flags())
	rootCmd.AddCommand(browseCommand(version, build))
	rootCmd.SetArgs(args)

	return rootCmd.Execute()
}

// browseCommand takes the same settings as the root command, so it computes
// the server identity a launch with those settings would.
func browseCommand(version, build string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "browse",
		Short: "Open the Web UI for these settings in the browser",
		Long: "Find or start the shared server for these settings, start its Web UI if it is not running, " +
			"and print the Web UI URL; browse then opens the browser on it unless --no-open is set. " +
			"The shared server exits once there are no attached sessions and no viewers and its linger runs out. " +
			"Under --server-mode session, and on Windows, browse instead builds the index itself, serves the Web UI " +
			"on 127.0.0.1 on an ephemeral port, prints its URL, and runs until interrupted. " +
			"On Windows browse prints the URL and does not open a browser. " +
			"The SSE and authentication flags of an MCP launch (--host, --port, --ui-enabled and the auth flags) " +
			"are accepted and ignored.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.RunBrowse(cmd.Context(), app.BrowseRunParams(cmd.Flags()), cmd.Flags(), binary(version, build))
		},
	}
	app.RegisterBrowseFlags(cmd.Flags())
	return cmd
}

func runWithFlags(flags *pflag.FlagSet, bin identity.Binary) error {
	return app.RunWithDeps(context.Background(), app.DefaultRunParams(), flags, bin)
}

func binary(version, build string) identity.Binary {
	// An unresolvable executable must not fail the launch: only an unstamped
	// build hashes it, and that identity is then uncomputable.
	executable, err := os.Executable()
	if err != nil {
		executable = ""
	}
	return identity.Binary{Version: version, Build: build, Executable: executable}
}
