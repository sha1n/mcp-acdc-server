//go:build unix

package app

import (
	"reflect"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const spawnTestIdentity = "fedcba9876543210fedcba9876543210"

func TestSharedServerCommand_ReExecsTheSameLaunchDetached(t *testing.T) {
	args := []string{"--content-dir", "/content", "--shared-server-linger", "5s"}

	cmd := sharedServerCommand("/opt/acdc/acdc-mcp", args, spawnTestIdentity)

	assert.Equal(t, "/opt/acdc/acdc-mcp", cmd.Path)
	assert.Equal(t, []string{"/opt/acdc/acdc-mcp", "--shared-server-identity", spawnTestIdentity, "--content-dir", "/content", "--shared-server-linger", "5s"}, cmd.Args)
	assert.Nil(t, cmd.Env, "the environment is inherited unscrubbed")
	assert.Empty(t, cmd.Dir, "the working directory is inherited")
	assert.Nil(t, cmd.Stdin)
	assert.Nil(t, cmd.Stdout)
	assert.Nil(t, cmd.Stderr)
	require.NotNil(t, cmd.SysProcAttr)
	assert.True(t, cmd.SysProcAttr.Setsid)
	assert.Equal(t, []string{"--content-dir", "/content", "--shared-server-linger", "5s"}, args, "the launch arguments are not modified")
}

func TestSharedServerCommand_TheIdentityFlagParsesForEveryLaunchShape(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantContentDir string
		wantPositional []string
	}{
		{name: "no arguments", args: nil, wantContentDir: ""},
		{name: "flags with separate values", args: []string{"--content-dir", "/c", "--transport", "stdio"}, wantContentDir: "/c"},
		{name: "flags with inline values", args: []string{"--content-dir=/c", "--server-mode=shared"}, wantContentDir: "/c"},
		{name: "boolean flag", args: []string{"--cross-ref", "--content-dir", "/c"}, wantContentDir: "/c"},
		{name: "positional argument", args: []string{"--content-dir", "/c", "extra"}, wantContentDir: "/c", wantPositional: []string{"extra"}},
		{name: "argument terminator", args: []string{"--content-dir", "/c", "--", "--not-a-flag"}, wantContentDir: "/c", wantPositional: []string{"--not-a-flag"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := sharedServerCommand("/bin/acdc-mcp", tt.args, spawnTestIdentity)

			var parsed *pflag.FlagSet
			var positional []string
			root := &cobra.Command{Use: "acdc-mcp", RunE: func(c *cobra.Command, a []string) error {
				parsed, positional = c.Flags(), a
				return nil
			}}
			RegisterFlags(root.Flags())
			root.SetArgs(cmd.Args[1:])
			require.NoError(t, root.Execute())

			id, supplied := sharedServerIdentity(parsed)
			assert.True(t, supplied)
			assert.Equal(t, spawnTestIdentity, id)
			contentDir, err := parsed.GetString("content-dir")
			require.NoError(t, err)
			assert.Equal(t, tt.wantContentDir, contentDir)
			assert.Equal(t, tt.wantPositional, nilIfEmpty(positional))
		})
	}
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func TestDefaultRunParams_CarriesTheReExecSpawn(t *testing.T) {
	spawn := DefaultRunParams().SpawnSharedServer

	require.NotNil(t, spawn)
	assert.Equal(t, reflect.ValueOf(spawnSharedServer).Pointer(), reflect.ValueOf(spawn).Pointer())
}
