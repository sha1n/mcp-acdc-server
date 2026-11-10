package config

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func validUISettings() *Settings {
	return &Settings{
		Transport: "sse",
		Scheme:    "acdc",
		Search:    defaultSearchSettings(),
		Auth:      AuthSettings{Type: AuthTypeNone},
		UI:        UISettings{Enabled: true, MaxResults: 20},
	}
}

func TestLoadSettings_UIDefaults(t *testing.T) {
	unsetEnvForTest(t, "ACDC_MCP_UI_ENABLED", "ACDC_MCP_UI_MAX_RESULTS")

	settings, err := LoadSettings()

	require.NoError(t, err)
	require.False(t, settings.UI.Enabled)
	require.Equal(t, 20, settings.UI.MaxResults)
}

func TestLoadSettings_UIEnvVars(t *testing.T) {
	t.Setenv("ACDC_MCP_UI_ENABLED", "true")
	t.Setenv("ACDC_MCP_UI_MAX_RESULTS", "35")

	settings, err := LoadSettings()

	require.NoError(t, err)
	require.True(t, settings.UI.Enabled)
	require.Equal(t, 35, settings.UI.MaxResults)
}

func TestLoadSettingsWithFlags_UIFlags(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("ui-enabled", false, "")
	flags.Int("ui-max-results", 0, "")
	require.NoError(t, flags.Set("ui-enabled", "true"))
	require.NoError(t, flags.Set("ui-max-results", "50"))

	settings, err := LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.True(t, settings.UI.Enabled)
	require.Equal(t, 50, settings.UI.MaxResults)
}

func TestLoadSettingsWithFlags_UIFlagsOverrideEnv(t *testing.T) {
	t.Setenv("ACDC_MCP_UI_ENABLED", "false")
	t.Setenv("ACDC_MCP_UI_MAX_RESULTS", "35")
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("ui-enabled", false, "")
	flags.Int("ui-max-results", 0, "")
	require.NoError(t, flags.Set("ui-enabled", "true"))
	require.NoError(t, flags.Set("ui-max-results", "7"))

	settings, err := LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.True(t, settings.UI.Enabled)
	require.Equal(t, 7, settings.UI.MaxResults)
}

func TestLoadSettingsWithFlags_UnsetUIFlagsKeepDefaults(t *testing.T) {
	unsetEnvForTest(t, "ACDC_MCP_UI_ENABLED", "ACDC_MCP_UI_MAX_RESULTS")
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("ui-enabled", false, "")
	flags.Int("ui-max-results", 0, "")

	settings, err := LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.False(t, settings.UI.Enabled)
	require.Equal(t, 20, settings.UI.MaxResults)
}

func TestMaxUIResultLimit(t *testing.T) {
	require.Equal(t, 100, MaxUIResultLimit)
}

func TestValidateSettings_UIMaxResults(t *testing.T) {
	tests := []struct {
		name       string
		maxResults int
		wantErr    bool
	}{
		{"lower bound", 1, false},
		{"default", 20, false},
		{"upper bound", MaxUIResultLimit, false},
		{"zero", 0, true},
		{"negative", -5, true},
		{"above upper bound", MaxUIResultLimit + 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validUISettings()
			s.UI.MaxResults = tt.maxResults

			err := ValidateSettings(s)

			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "ui.max_results")
				return
			}
			require.NoError(t, err)
		})
	}
}

// The UI over stdio is a misconfiguration the server tolerates with a warning.
func TestValidateSettings_UIEnabledWithStdioIsNotAnError(t *testing.T) {
	s := validUISettings()
	s.Transport = "stdio"

	require.NoError(t, ValidateSettings(s))
}
