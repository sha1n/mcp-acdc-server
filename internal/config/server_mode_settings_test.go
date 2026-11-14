package config

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var serverModeEnvVars = []string{
	"ACDC_MCP_SERVER_MODE",
	"ACDC_MCP_SHARED_SERVER_CONNECT_WAIT",
	"ACDC_MCP_SHARED_SERVER_LINGER",
}

func defaultSharedServerSettings() SharedServerSettings {
	return SharedServerSettings{
		ConnectWait: DefaultSharedServerConnectWait,
		Linger:      DefaultSharedServerLinger,
	}
}

func newServerModeFlagSet(t *testing.T, values map[string]string) *pflag.FlagSet {
	t.Helper()

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("server-mode", "", "")
	flags.String("shared-server-connect-wait", "", "")
	flags.String("shared-server-linger", "", "")
	for name, value := range values {
		require.NoError(t, flags.Set(name, value))
	}
	return flags
}

type serverModeSource struct {
	env     map[string]string
	file    string
	flags   map[string]string
	noFlags bool
}

func loadServerModeSettings(t *testing.T, src serverModeSource) (*Settings, error) {
	t.Helper()

	t.Chdir(t.TempDir())
	unsetEnvForTest(t, serverModeEnvVars...)
	for key, value := range src.env {
		t.Setenv(key, value)
	}
	if src.file != "" {
		require.NoError(t, os.WriteFile(".env", []byte(src.file), 0o600))
	}
	var flags *pflag.FlagSet
	if !src.noFlags {
		flags = newServerModeFlagSet(t, src.flags)
	}

	return LoadSettingsWithFlags(flags)
}

func TestServerModeSettings_Defaults(t *testing.T) {
	assert.Equal(t, ServerMode("shared"), ServerModeShared)
	assert.Equal(t, ServerMode("session"), ServerModeSession)
	assert.Equal(t, 5*time.Second, DefaultSharedServerConnectWait)
	assert.Equal(t, 30*time.Second, DefaultSharedServerLinger)
}

func TestLoadSettingsWithFlags_ServerModeSettingsPrecedence(t *testing.T) {
	tests := []struct {
		name         string
		src          serverModeSource
		wantMode     ServerMode
		wantSettings SharedServerSettings
	}{
		{
			name:         "defaults without flags",
			src:          serverModeSource{noFlags: true},
			wantMode:     ServerModeShared,
			wantSettings: SharedServerSettings{ConnectWait: 5 * time.Second, Linger: 30 * time.Second},
		},
		{
			name:         "unset flags keep defaults",
			wantMode:     ServerModeShared,
			wantSettings: SharedServerSettings{ConnectWait: 5 * time.Second, Linger: 30 * time.Second},
		},
		{
			name: "env vars",
			src: serverModeSource{noFlags: true, env: map[string]string{
				"ACDC_MCP_SERVER_MODE":                "session",
				"ACDC_MCP_SHARED_SERVER_CONNECT_WAIT": "500ms",
				"ACDC_MCP_SHARED_SERVER_LINGER":       "1m",
			}},
			wantMode:     ServerModeSession,
			wantSettings: SharedServerSettings{ConnectWait: 500 * time.Millisecond, Linger: time.Minute},
		},
		{
			name: "unset flags keep env values",
			src: serverModeSource{env: map[string]string{
				"ACDC_MCP_SERVER_MODE":                "session",
				"ACDC_MCP_SHARED_SERVER_CONNECT_WAIT": "2s",
				"ACDC_MCP_SHARED_SERVER_LINGER":       "45s",
			}},
			wantMode:     ServerModeSession,
			wantSettings: SharedServerSettings{ConnectWait: 2 * time.Second, Linger: 45 * time.Second},
		},
		{
			name: "config file keys",
			src: serverModeSource{
				file: "server_mode=session\nshared_server.connect_wait=3s\nshared_server.linger=2m\n",
			},
			wantMode:     ServerModeSession,
			wantSettings: SharedServerSettings{ConnectWait: 3 * time.Second, Linger: 2 * time.Minute},
		},
		{
			name: "env beats config file",
			src: serverModeSource{
				env:  map[string]string{"ACDC_MCP_SHARED_SERVER_LINGER": "40s"},
				file: "server_mode=session\nshared_server.connect_wait=3s\nshared_server.linger=2m\n",
			},
			wantMode:     ServerModeSession,
			wantSettings: SharedServerSettings{ConnectWait: 3 * time.Second, Linger: 40 * time.Second},
		},
		{
			name: "flags",
			src: serverModeSource{flags: map[string]string{
				"server-mode":                "session",
				"shared-server-connect-wait": "750ms",
				"shared-server-linger":       "10s",
			}},
			wantMode:     ServerModeSession,
			wantSettings: SharedServerSettings{ConnectWait: 750 * time.Millisecond, Linger: 10 * time.Second},
		},
		{
			name: "flags beat env",
			src: serverModeSource{
				env: map[string]string{
					"ACDC_MCP_SERVER_MODE":                "session",
					"ACDC_MCP_SHARED_SERVER_CONNECT_WAIT": "2s",
					"ACDC_MCP_SHARED_SERVER_LINGER":       "45s",
				},
				flags: map[string]string{
					"server-mode":                "shared",
					"shared-server-connect-wait": "1s",
					"shared-server-linger":       "5s",
				},
			},
			wantMode:     ServerModeShared,
			wantSettings: SharedServerSettings{ConnectWait: time.Second, Linger: 5 * time.Second},
		},
		{
			name: "flags beat config file",
			src: serverModeSource{
				file: "server_mode=session\nshared_server.connect_wait=3s\nshared_server.linger=2m\n",
				flags: map[string]string{
					"server-mode":                "shared",
					"shared-server-connect-wait": "4s",
					"shared-server-linger":       "90s",
				},
			},
			wantMode:     ServerModeShared,
			wantSettings: SharedServerSettings{ConnectWait: 4 * time.Second, Linger: 90 * time.Second},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings, err := loadServerModeSettings(t, tt.src)

			require.NoError(t, err)
			assert.Equal(t, tt.wantMode, settings.ServerMode)
			assert.Equal(t, tt.wantSettings, settings.SharedServer)
		})
	}
}

func TestLoadSettingsWithFlags_SharedServerDurationRejectsBareNumber(t *testing.T) {
	tests := []struct {
		name    string
		src     serverModeSource
		wantKey string
	}{
		{
			name:    "connect wait flag",
			src:     serverModeSource{flags: map[string]string{"shared-server-connect-wait": "5"}},
			wantKey: "shared_server.connect_wait",
		},
		{
			name:    "connect wait env var",
			src:     serverModeSource{noFlags: true, env: map[string]string{"ACDC_MCP_SHARED_SERVER_CONNECT_WAIT": "5"}},
			wantKey: "shared_server.connect_wait",
		},
		{
			name:    "connect wait config key",
			src:     serverModeSource{file: "shared_server.connect_wait=5\n"},
			wantKey: "shared_server.connect_wait",
		},
		{
			name:    "linger flag",
			src:     serverModeSource{flags: map[string]string{"shared-server-linger": "5"}},
			wantKey: "shared_server.linger",
		},
		{
			name:    "linger env var",
			src:     serverModeSource{noFlags: true, env: map[string]string{"ACDC_MCP_SHARED_SERVER_LINGER": "5"}},
			wantKey: "shared_server.linger",
		},
		{
			name:    "linger config key",
			src:     serverModeSource{file: "shared_server.linger=5\n"},
			wantKey: "shared_server.linger",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadServerModeSettings(t, tt.src)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantKey)
			assert.Contains(t, err.Error(), "Go duration")
		})
	}
}

func validServerModeBaseSettings(transport string, mode ServerMode) *Settings {
	return &Settings{
		Transport:    transport,
		ServerMode:   mode,
		SharedServer: defaultSharedServerSettings(),
		Scheme:       "acdc",
		Search:       defaultSearchSettings(),
		Auth:         AuthSettings{Type: AuthTypeNone},
	}
}

// Every case runs under each transport and each valid base mode, so validation
// is pinned as independent of whether the settings have any effect.
func TestValidateSettings_ServerModeSettings(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Settings)
		wantErr string
	}{
		{name: "defaults", mutate: func(*Settings) {}},
		{name: "mode shared", mutate: func(s *Settings) { s.ServerMode = ServerModeShared }},
		{name: "mode session", mutate: func(s *Settings) { s.ServerMode = ServerModeSession }},
		{name: "unknown mode", mutate: func(s *Settings) { s.ServerMode = "strict" }, wantErr: "server_mode"},
		{name: "empty mode", mutate: func(s *Settings) { s.ServerMode = "" }, wantErr: "server_mode"},
		{name: "connect wait 0", mutate: func(s *Settings) { s.SharedServer.ConnectWait = 0 }, wantErr: "shared_server.connect_wait"},
		{name: "connect wait -1s", mutate: func(s *Settings) { s.SharedServer.ConnectWait = -time.Second }, wantErr: "shared_server.connect_wait"},
		{name: "connect wait 500ms", mutate: func(s *Settings) { s.SharedServer.ConnectWait = 500 * time.Millisecond }},
		{name: "connect wait 1ns", mutate: func(s *Settings) { s.SharedServer.ConnectWait = time.Nanosecond }},
		{name: "connect wait has no maximum", mutate: func(s *Settings) { s.SharedServer.ConnectWait = 24 * time.Hour }},
		{name: "linger 999ms", mutate: func(s *Settings) { s.SharedServer.Linger = 999 * time.Millisecond }, wantErr: "shared_server.linger"},
		{name: "linger 0", mutate: func(s *Settings) { s.SharedServer.Linger = 0 }, wantErr: "shared_server.linger"},
		{name: "linger -1s", mutate: func(s *Settings) { s.SharedServer.Linger = -time.Second }, wantErr: "shared_server.linger"},
		{name: "linger 1s", mutate: func(s *Settings) { s.SharedServer.Linger = time.Second }},
	}

	for _, transport := range []string{"stdio", "sse"} {
		for _, mode := range []ServerMode{ServerModeShared, ServerModeSession} {
			for _, tt := range tests {
				t.Run(transport+"/"+string(mode)+"/"+tt.name, func(t *testing.T) {
					s := validServerModeBaseSettings(transport, mode)
					tt.mutate(s)

					err := ValidateSettings(s)

					if tt.wantErr == "" {
						require.NoError(t, err)
						return
					}
					require.Error(t, err)
					assert.Contains(t, err.Error(), tt.wantErr)
				})
			}
		}
	}
}

func TestValidateSettings_SharedModeWithSSEIsAccepted(t *testing.T) {
	require.NoError(t, ValidateSettings(validServerModeBaseSettings("sse", ServerModeShared)))
}

func TestValidateSettings_AuthValidatedInEveryServerMode(t *testing.T) {
	for _, transport := range []string{"stdio", "sse"} {
		for _, mode := range []ServerMode{ServerModeShared, ServerModeSession} {
			t.Run(transport+"/"+string(mode), func(t *testing.T) {
				s := validServerModeBaseSettings(transport, mode)
				s.Auth = AuthSettings{Type: AuthTypeBasic}

				err := ValidateSettings(s)

				require.Error(t, err)
				assert.Contains(t, err.Error(), "auth-type 'basic' requires")
			})
		}
	}
}

func TestLogWithLogger_LogsServerModeSettings(t *testing.T) {
	for _, transport := range []string{"stdio", "sse"} {
		t.Run(transport, func(t *testing.T) {
			captured := make(map[string]any)
			s := validServerModeBaseSettings(transport, ServerModeSession)
			s.SharedServer = SharedServerSettings{ConnectWait: 750 * time.Millisecond, Linger: 2 * time.Minute}

			LogWithLogger(s, slog.New(&mapHandler{attrs: captured}))

			assert.Equal(t, ServerModeSession, captured["Config: server_mode"])
			assert.Equal(t, 750*time.Millisecond, captured["Config: shared_server.connect_wait"])
			assert.Equal(t, 2*time.Minute, captured["Config: shared_server.linger"])
		})
	}
}

// time.ParseDuration accepts a unitless zero, so these would otherwise slip past
// the format check and surface as a range error that does not name the format.
func TestLoadSettingsWithFlags_SharedServerDurationRejectsUnitlessZero(t *testing.T) {
	keys := []struct {
		key  string
		flag string
		env  string
	}{
		{"shared_server.connect_wait", "shared-server-connect-wait", "ACDC_MCP_SHARED_SERVER_CONNECT_WAIT"},
		{"shared_server.linger", "shared-server-linger", "ACDC_MCP_SHARED_SERVER_LINGER"},
	}

	for _, k := range keys {
		for _, value := range []string{"0", "+0", "-0"} {
			sources := map[string]serverModeSource{
				"flag":       {flags: map[string]string{k.flag: value}},
				"env var":    {noFlags: true, env: map[string]string{k.env: value}},
				"config key": {file: k.key + "=" + value + "\n"},
			}
			for sourceName, src := range sources {
				t.Run(k.key+"/"+sourceName+"/"+value, func(t *testing.T) {
					_, err := loadServerModeSettings(t, src)

					require.Error(t, err)
					assert.Contains(t, err.Error(), k.key)
					assert.Contains(t, err.Error(), "Go duration")
				})
			}
		}
	}
}
