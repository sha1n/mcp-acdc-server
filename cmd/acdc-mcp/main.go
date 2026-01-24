package main

import (
	"context"
	"os"

	"github.com/sha1n/mcp-acdc-server/internal/app"
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
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWithFlags(cmd.Flags(), version)
		},
	}

	rootCmd.SetVersionTemplate(`{{.Version}}
`)

	app.RegisterFlags(rootCmd.Flags())
	rootCmd.SetArgs(args)

	return rootCmd.Execute()
}

func runWithFlags(flags *pflag.FlagSet, version string) error {
	return app.RunWithDeps(context.Background(), app.DefaultRunParams(), flags, version)
}
