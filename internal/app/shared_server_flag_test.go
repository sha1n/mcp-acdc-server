package app

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sharedServerFlagTestIdentity = "0123456789abcdef0123456789abcdef"

func TestRegisterFlags_SharedServerIdentityIsHiddenFromHelp(t *testing.T) {
	cmd := &cobra.Command{Use: "acdc-mcp", RunE: func(*cobra.Command, []string) error { return nil }}
	RegisterFlags(cmd.Flags())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--help"})

	require.NoError(t, cmd.Execute())

	require.NotNil(t, cmd.Flags().Lookup("shared-server-identity"), "the flag must be registered")
	assert.NotContains(t, out.String(), "shared-server-identity")
	assert.Contains(t, out.String(), "--content-dir", "help output must still list public flags")
}

func TestRunWithDeps_SharedServerIdentityEnvironmentVariableHasNoEffect(t *testing.T) {
	endpointDir := t.TempDir()
	t.Setenv("ACDC_MCP_SHARED_SERVER_IDENTITY", sharedServerFlagTestIdentity)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NoError(t, flags.Set("content-dir", t.TempDir()))
	require.NoError(t, flags.Set("transport", "stdio"))

	stdioUsed := false
	params := RunParams{
		LoadSettings:  config.LoadSettingsWithFlags,
		ValidSettings: config.ValidateSettings,
		CreateServer: func(context.Context, *config.Settings, string) (Components, error) {
			return Components{MCP: mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)}, nil
		},
		CustomIOTransport: &mockTransport{connectCalled: &stdioUsed},
		EndpointDir:       func() (string, error) { return endpointDir, nil },
	}

	_ = RunWithDeps(context.Background(), params, flags, "test")

	assert.True(t, stdioUsed, "an ordinary stdio launch must run")
	entries, err := os.ReadDir(endpointDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "the shared server path must not be reached through the environment")
}

func TestDefaultRunParams_ProvidesEndpointDir(t *testing.T) {
	assert.NotNil(t, DefaultRunParams().EndpointDir)
}

func TestRunWithDeps_SuppliedSharedServerIdentityMustBeValid(t *testing.T) {
	tests := []struct {
		name     string
		identity string
	}{
		{name: "explicit empty", identity: ""},
		{name: "too short", identity: "0123456789abcdef"},
		{name: "uppercase hex", identity: "0123456789ABCDEF0123456789ABCDEF"},
		{name: "non-hex", identity: "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpointDir := t.TempDir()
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			RegisterFlags(flags)
			require.NoError(t, flags.Set("content-dir", t.TempDir()))
			require.NoError(t, flags.Set("transport", "stdio"))
			require.NoError(t, flags.Parse([]string{"--shared-server-identity=" + tt.identity}))

			created := false
			stdioUsed := false
			params := RunParams{
				LoadSettings:  config.LoadSettingsWithFlags,
				ValidSettings: config.ValidateSettings,
				CreateServer: func(context.Context, *config.Settings, string) (Components, error) {
					created = true
					return Components{MCP: mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)}, nil
				},
				CustomIOTransport: &mockTransport{connectCalled: &stdioUsed},
				EndpointDir:       func() (string, error) { return endpointDir, nil },
			}

			err := RunWithDeps(context.Background(), params, flags, "test")

			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid shared server identity")
			assert.False(t, created, "no server may be built for a malformed identity")
			assert.False(t, stdioUsed, "an ordinary launch must not start")
			entries, err := os.ReadDir(endpointDir)
			require.NoError(t, err)
			assert.Empty(t, entries, "nothing may be written for a malformed identity")
		})
	}
}
