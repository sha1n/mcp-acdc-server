package config

import (
	"log/slog"
	"os"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUISettings_Constants(t *testing.T) {
	assert.Equal(t, 100, MaxUIResultLimit)
	assert.Equal(t, 20, DefaultUIMaxResults)
}

func newUIFlagSet(t *testing.T, values map[string]string) *pflag.FlagSet {
	t.Helper()

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("ui-enabled", false, "")
	flags.Int("ui-max-results", 0, "")
	for name, value := range values {
		require.NoError(t, flags.Set(name, value))
	}
	return flags
}

func TestLoadSettingsWithFlags_UISettingsPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		file     string
		flags    map[string]string
		noFlags  bool
		expected UISettings
	}{
		{
			name:     "defaults without flags",
			noFlags:  true,
			expected: UISettings{Enabled: false, MaxResults: DefaultUIMaxResults},
		},
		{
			name:     "unset flags keep defaults",
			expected: UISettings{Enabled: false, MaxResults: DefaultUIMaxResults},
		},
		{
			name:     "env vars",
			env:      map[string]string{"ACDC_MCP_UI_ENABLED": "true", "ACDC_MCP_UI_MAX_RESULTS": "50"},
			noFlags:  true,
			expected: UISettings{Enabled: true, MaxResults: 50},
		},
		{
			name:     "unset flags keep env values",
			env:      map[string]string{"ACDC_MCP_UI_ENABLED": "true", "ACDC_MCP_UI_MAX_RESULTS": "50"},
			expected: UISettings{Enabled: true, MaxResults: 50},
		},
		{
			name:     "config file",
			file:     "ui.enabled=true\nui.max_results=30\n",
			expected: UISettings{Enabled: true, MaxResults: 30},
		},
		{
			name:     "env beats config file",
			env:      map[string]string{"ACDC_MCP_UI_MAX_RESULTS": "40"},
			file:     "ui.enabled=true\nui.max_results=30\n",
			expected: UISettings{Enabled: true, MaxResults: 40},
		},
		{
			name:     "flags",
			flags:    map[string]string{"ui-enabled": "true", "ui-max-results": "70"},
			expected: UISettings{Enabled: true, MaxResults: 70},
		},
		{
			name:     "flags beat env",
			env:      map[string]string{"ACDC_MCP_UI_ENABLED": "true", "ACDC_MCP_UI_MAX_RESULTS": "50"},
			flags:    map[string]string{"ui-enabled": "false", "ui-max-results": "70"},
			expected: UISettings{Enabled: false, MaxResults: 70},
		},
		{
			name:     "flags beat config file",
			file:     "ui.enabled=false\nui.max_results=30\n",
			flags:    map[string]string{"ui-enabled": "true", "ui-max-results": "5"},
			expected: UISettings{Enabled: true, MaxResults: 5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			unsetEnvForTest(t, "ACDC_MCP_UI_ENABLED", "ACDC_MCP_UI_MAX_RESULTS")
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			if tt.file != "" {
				require.NoError(t, os.WriteFile(".env", []byte(tt.file), 0o600))
			}
			var flags *pflag.FlagSet
			if !tt.noFlags {
				flags = newUIFlagSet(t, tt.flags)
			}

			settings, err := LoadSettingsWithFlags(flags)

			require.NoError(t, err)
			assert.Equal(t, tt.expected, settings.UI)
		})
	}
}

func TestLoadSettings_UIMaxResultsInvalidEnv(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("ACDC_MCP_UI_MAX_RESULTS", "not-a-number")

	_, err := LoadSettings()

	require.Error(t, err)
}

func validUIBaseSettings(ui UISettings) *Settings {
	return &Settings{
		Transport: "sse",
		Scheme:    "acdc",
		Search:    defaultSearchSettings(),
		Auth:      AuthSettings{Type: AuthTypeNone},
		UI:        ui,
	}
}

func TestValidateSettings_UIMaxResults(t *testing.T) {
	tests := []struct {
		name    string
		ui      UISettings
		wantErr bool
	}{
		{name: "enabled zero", ui: UISettings{Enabled: true, MaxResults: 0}, wantErr: true},
		{name: "enabled negative", ui: UISettings{Enabled: true, MaxResults: -1}, wantErr: true},
		{name: "enabled lower bound", ui: UISettings{Enabled: true, MaxResults: 1}},
		{name: "enabled default", ui: UISettings{Enabled: true, MaxResults: DefaultUIMaxResults}},
		{name: "enabled upper bound", ui: UISettings{Enabled: true, MaxResults: MaxUIResultLimit}},
		{name: "enabled above upper bound", ui: UISettings{Enabled: true, MaxResults: MaxUIResultLimit + 1}, wantErr: true},
		{name: "disabled zero", ui: UISettings{Enabled: false, MaxResults: 0}},
		{name: "disabled negative", ui: UISettings{Enabled: false, MaxResults: -1}},
		{name: "disabled above upper bound", ui: UISettings{Enabled: false, MaxResults: MaxUIResultLimit + 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSettings(validUIBaseSettings(tt.ui))

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "ui.max_results")
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestValidateSettings_UIEnabledOnStdioIsValid(t *testing.T) {
	s := validUIBaseSettings(UISettings{Enabled: true, MaxResults: DefaultUIMaxResults})
	s.Transport = "stdio"

	assert.NoError(t, ValidateSettings(s))
}

func TestValidateSettings_UIRuleDoesNotMaskOtherErrors(t *testing.T) {
	s := validUIBaseSettings(UISettings{Enabled: true, MaxResults: DefaultUIMaxResults})
	s.Transport = "bogus"

	err := ValidateSettings(s)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "transport")
}

func TestUISettingsLogValue_Group(t *testing.T) {
	tests := []struct {
		name string
		ui   UISettings
	}{
		{name: "enabled", ui: UISettings{Enabled: true, MaxResults: 42}},
		{name: "disabled", ui: UISettings{Enabled: false, MaxResults: 7}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val := UISettingsLogValue(tt.ui)

			require.Equal(t, slog.KindGroup, val.Kind())
			attrs := groupAttrs(val)
			require.Len(t, attrs, 2)
			require.Contains(t, attrs, "enabled")
			require.Contains(t, attrs, "max_results")
			assert.Equal(t, tt.ui.Enabled, attrs["enabled"].Bool())
			assert.Equal(t, int64(tt.ui.MaxResults), attrs["max_results"].Int64())
		})
	}
}

func groupAttrs(v slog.Value) map[string]slog.Value {
	attrs := make(map[string]slog.Value)
	for _, a := range v.Group() {
		attrs[a.Key] = a.Value
	}
	return attrs
}
