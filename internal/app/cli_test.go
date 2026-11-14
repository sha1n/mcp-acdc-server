package app

import (
	"os"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestRegisterFlags_AllFlagsExist(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)

	expectedFlags := []struct {
		long  string
		short string
	}{
		{"content-dir", "c"},
		{"transport", "t"},
		{"host", "H"},
		{"port", "p"},
		{"search-max-results", "m"},
		{"search-result-mode", ""},
		{"auth-type", "a"},
		{"auth-basic-username", "u"},
		{"auth-basic-password", "P"},
		{"auth-api-keys", "k"},
	}

	for _, ef := range expectedFlags {
		f := flags.Lookup(ef.long)
		if f == nil {
			t.Errorf("Flag --%s not registered", ef.long)
			continue
		}
		if f.Shorthand != ef.short {
			t.Errorf("Flag --%s has wrong shorthand: expected -%s, got -%s", ef.long, ef.short, f.Shorthand)
		}
	}
}

func TestCLI_FlagParsing_SearchResultMode(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NoError(t, flags.Set("search-result-mode", "content"))
	settings, err := config.LoadSettingsWithFlags(flags)
	require.NoError(t, err)
	require.Equal(t, config.SearchResultModeContent, settings.Search.ResultMode)
}

func TestRegisterFlags_FlagDescriptions(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)

	// Verify all flags have non-empty descriptions
	flags.VisitAll(func(f *pflag.Flag) {
		if f.Usage == "" {
			t.Errorf("Flag --%s has empty description", f.Name)
		}
	})
}

func TestCLI_FlagParsing_ContentDir(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)

	err := flags.Parse([]string{"--content-dir=/custom/path"})
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	val, err := flags.GetString("content-dir")
	if err != nil {
		t.Fatalf("GetString failed: %v", err)
	}
	if val != "/custom/path" {
		t.Errorf("Expected '/custom/path', got '%s'", val)
	}
}

func TestCLI_FlagParsing_ShortFlags(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)

	err := flags.Parse([]string{"-t", "stdio", "-p", "9000", "-a", "basic"})
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	transport, _ := flags.GetString("transport")
	port, _ := flags.GetInt("port")
	authType, _ := flags.GetString("auth-type")

	if transport != "stdio" {
		t.Errorf("Expected transport 'stdio', got '%s'", transport)
	}
	if port != 9000 {
		t.Errorf("Expected port 9000, got %d", port)
	}
	if authType != "basic" {
		t.Errorf("Expected auth type 'basic', got '%s'", authType)
	}
}

func TestCLI_FlagParsing_AuthAPIKeys(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)

	err := flags.Parse([]string{"--auth-api-keys=key1,key2,key3"})
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	keys, err := flags.GetStringSlice("auth-api-keys")
	if err != nil {
		t.Fatalf("GetStringSlice failed: %v", err)
	}

	if len(keys) != 3 {
		t.Fatalf("Expected 3 keys, got %d", len(keys))
	}
	if keys[0] != "key1" || keys[1] != "key2" || keys[2] != "key3" {
		t.Errorf("Unexpected keys: %v", keys)
	}
}

func TestRegisterFlags_SearchTitleBoostReplacesNameBoost(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)

	require.NotNil(t, flags.Lookup("search-title-boost"))
	require.Nil(t, flags.Lookup("search-name-boost"))
}

func TestCLI_FlagParsing_SearchTitleBoost(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NoError(t, flags.Set("search-title-boost", "7.5"))

	settings, err := config.LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.Equal(t, 7.5, settings.Search.TitleBoost)
}

func TestCLI_FlagParsing_SearchHeadingBoost(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NoError(t, flags.Set("search-heading-boost", "7.5"))

	settings, err := config.LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.Equal(t, 7.5, settings.Search.HeadingBoost)
}

func TestCLI_FlagParsing_SearchPathBoost(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NoError(t, flags.Set("search-path-boost", "7.5"))

	settings, err := config.LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.Equal(t, 7.5, settings.Search.PathBoost)
}

func TestCLI_FlagParsing_SearchInMemory(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NoError(t, flags.Set("search-in-memory", "false"))

	settings, err := config.LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.False(t, settings.Search.InMemory)
}

// TestCLI_SearchInMemoryDefaultsToTrueWhenFlagIsNotPassed pins the priority
// this setting depends on: registering a bool flag must not itself decide the
// index kind. Viper reads a bound flag only once pflag reports it changed, and
// falls back to SetDefault before the flag's own default value — measured, so
// the flag's default is cosmetic and shows only in --help. Registering the
// flag would otherwise be a silent way to flip a default set elsewhere.
func TestCLI_SearchInMemoryDefaultsToTrueWhenFlagIsNotPassed(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)

	settings, err := config.LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.True(t, settings.Search.InMemory)
}

func TestRegisterFlags_UIFlags(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)

	tests := []struct {
		name       string
		valueType  string
		defValue   string
		usageMatch string
	}{
		{name: "ui-enabled", valueType: "bool", defValue: "false"},
		{name: "ui-max-results", valueType: "int", defValue: "0", usageMatch: "(default: 20)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := flags.Lookup(tt.name)
			require.NotNil(t, f, "flag --%s not registered", tt.name)
			require.Equal(t, tt.valueType, f.Value.Type())
			require.Equal(t, tt.defValue, f.DefValue)
			require.Empty(t, f.Shorthand)
			require.NotEmpty(t, f.Usage)
			if tt.usageMatch != "" {
				require.Contains(t, f.Usage, tt.usageMatch)
			}
		})
	}
}

func TestCLI_FlagParsing_UISettings(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		args     []string
		expected config.UISettings
	}{
		{
			name:     "no flags keep defaults",
			expected: config.UISettings{Enabled: false, MaxResults: config.DefaultUIMaxResults},
		},
		{
			name:     "flags set both keys",
			args:     []string{"--ui-enabled", "--ui-max-results=55"},
			expected: config.UISettings{Enabled: true, MaxResults: 55},
		},
		{
			name:     "unset max results flag keeps default",
			args:     []string{"--ui-enabled"},
			expected: config.UISettings{Enabled: true, MaxResults: config.DefaultUIMaxResults},
		},
		{
			name:     "unset flags keep env values",
			env:      map[string]string{"ACDC_MCP_UI_ENABLED": "true", "ACDC_MCP_UI_MAX_RESULTS": "33"},
			expected: config.UISettings{Enabled: true, MaxResults: 33},
		},
		{
			name:     "flags beat env",
			env:      map[string]string{"ACDC_MCP_UI_ENABLED": "true", "ACDC_MCP_UI_MAX_RESULTS": "33"},
			args:     []string{"--ui-enabled=false", "--ui-max-results=77"},
			expected: config.UISettings{Enabled: false, MaxResults: 77},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("ACDC_MCP_UI_ENABLED", "")
			t.Setenv("ACDC_MCP_UI_MAX_RESULTS", "")
			require.NoError(t, os.Unsetenv("ACDC_MCP_UI_ENABLED"))
			require.NoError(t, os.Unsetenv("ACDC_MCP_UI_MAX_RESULTS"))
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			RegisterFlags(flags)
			require.NoError(t, flags.Parse(tt.args))

			settings, err := config.LoadSettingsWithFlags(flags)

			require.NoError(t, err)
			require.Equal(t, tt.expected, settings.UI)
		})
	}
}

func TestRegisterFlags_ServerModeFlagsHaveNoShortForm(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)

	for _, name := range []string{"server-mode", "shared-server-connect-wait", "shared-server-linger"} {
		f := flags.Lookup(name)
		require.NotNil(t, f, "flag --%s not registered", name)
		require.Empty(t, f.Shorthand, "flag --%s must have no short form", name)
		require.False(t, f.Hidden, "flag --%s must appear in --help", name)
	}
}

func TestCLI_FlagParsing_ServerModeSettings(t *testing.T) {
	t.Chdir(t.TempDir())
	clearServerModeEnv(t)
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)

	require.NoError(t, flags.Parse([]string{
		"--server-mode=session",
		"--shared-server-connect-wait=500ms",
		"--shared-server-linger=1m",
	}))
	settings, err := config.LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.Equal(t, config.ServerModeSession, settings.ServerMode)
	require.Equal(t, 500*time.Millisecond, settings.SharedServer.ConnectWait)
	require.Equal(t, time.Minute, settings.SharedServer.Linger)
}

func TestCLI_FlagParsing_ServerModeDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	clearServerModeEnv(t)
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)

	require.NoError(t, flags.Parse(nil))
	settings, err := config.LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.Equal(t, config.ServerModeShared, settings.ServerMode)
	require.Equal(t, 5*time.Second, settings.SharedServer.ConnectWait)
	require.Equal(t, 30*time.Second, settings.SharedServer.Linger)
}

// The bare-number error must come from settings loading, which names the Go
// duration format, not from the flag parser, which does not.
func TestCLI_FlagParsing_SharedServerDurationBareNumber(t *testing.T) {
	for _, name := range []string{"shared-server-connect-wait", "shared-server-linger"} {
		t.Run(name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			RegisterFlags(flags)

			require.NoError(t, flags.Parse([]string{"--" + name + "=5"}))
			_, err := config.LoadSettingsWithFlags(flags)

			require.Error(t, err)
			require.Contains(t, err.Error(), "Go duration")
		})
	}
}

// Viper treats an empty variable as unset, so t.Setenv to "" isolates the test
// from the caller's environment and restores it afterwards.
func clearServerModeEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"ACDC_MCP_SERVER_MODE", "ACDC_MCP_SHARED_SERVER_CONNECT_WAIT", "ACDC_MCP_SHARED_SERVER_LINGER"} {
		t.Setenv(key, "")
	}
}
