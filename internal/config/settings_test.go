package config

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func TestLoadSettings_Defaults(t *testing.T) {
	// Clear env vars to ensure defaults are used
	// Note: Can't use t.Setenv to clear, so we still need os.Unsetenv here
	_ = os.Unsetenv("ACDC_MCP_PORT")
	_ = os.Unsetenv("ACDC_MCP_AUTH_TYPE")

	settings, err := LoadSettings()
	if err != nil {
		t.Fatalf("Failed to load settings: %v", err)
	}

	if settings.Port != 8080 {
		t.Errorf("Expected default port 8080, got %d", settings.Port)
	}
	if settings.Auth.Type != AuthTypeNone {
		t.Errorf("Expected default auth type '%s', got '%s'", AuthTypeNone, settings.Auth.Type)
	}
}

func TestLoadSettings_EnvVars(t *testing.T) {
	t.Setenv("ACDC_MCP_PORT", "9090")
	t.Setenv("ACDC_MCP_AUTH_TYPE", "basic")
	t.Setenv("ACDC_MCP_AUTH_BASIC_USERNAME", "admin")

	settings, err := LoadSettings()
	if err != nil {
		t.Fatalf("Failed to load settings: %v", err)
	}

	if settings.Port != 9090 {
		t.Errorf("Expected port 9090, got %d", settings.Port)
	}
	if settings.Auth.Type != AuthTypeBasic {
		t.Errorf("Expected auth type '%s', got '%s'", AuthTypeBasic, settings.Auth.Type)
	}
	if settings.Auth.Basic.Username != "admin" {
		t.Errorf("Expected username 'admin', got '%s'", settings.Auth.Basic.Username)
	}
}

func TestLoadSettings_APIKeys_EnvVar(t *testing.T) {
	// Test the manual splitting logic
	t.Setenv("ACDC_MCP_AUTH_API_KEYS", "key1, key2,key3")

	settings, err := LoadSettings()
	if err != nil {
		t.Fatalf("Failed to load settings: %v", err)
	}

	if len(settings.Auth.APIKeys) != 3 {
		t.Fatalf("Expected 3 API keys, got %d", len(settings.Auth.APIKeys))
	}
	if settings.Auth.APIKeys[0] != "key1" {
		t.Errorf("Expected key1, got '%s'", settings.Auth.APIKeys[0])
	}
	if settings.Auth.APIKeys[1] != "key2" {
		t.Errorf("Expected key2, got '%s'", settings.Auth.APIKeys[1])
	}
	if settings.Auth.APIKeys[2] != "key3" {
		t.Errorf("Expected key3, got '%s'", settings.Auth.APIKeys[2])
	}
}

func TestLoadSettings_APIKeys_EnvVar_ViperSingleElement(t *testing.T) {
	// If we set a single key, it should work too
	t.Setenv("ACDC_MCP_AUTH_API_KEYS", "singlekey")

	settings, err := LoadSettings()
	if err != nil {
		t.Fatalf("Failed to load settings: %v", err)
	}
	if len(settings.Auth.APIKeys) != 1 {
		t.Fatalf("Expected 1 API key, got %d", len(settings.Auth.APIKeys))
	}
	if settings.Auth.APIKeys[0] != "singlekey" {
		t.Errorf("Expected singlekey, got '%s'", settings.Auth.APIKeys[0])
	}
}

func TestLoadSettings_EnvFile(t *testing.T) {
	// Create temporary .env file
	// Note: Viper config files use keys matching the mapstructure tags (or lowercase),
	// NOT the environment variable keys with prefixes.
	content := []byte("host=127.0.0.2\nport=7000")
	tmpEnv := ".env"
	if err := os.WriteFile(tmpEnv, content, 0644); err != nil {
		t.Fatalf("Failed to create .env file: %v", err)
	}
	defer func() { _ = os.Remove(tmpEnv) }()

	settings, err := LoadSettings()
	if err != nil {
		t.Fatalf("Failed to load settings: %v", err)
	}

	if settings.Host != "127.0.0.2" {
		t.Errorf("Expected host 127.0.0.2, got %s", settings.Host)
	}
	if settings.Port != 7000 {
		t.Errorf("Expected port 7000, got %d", settings.Port)
	}
}

func TestLoadSettings_InvalidConfig(t *testing.T) {
	// Create invalid env var type (Port expects int)
	t.Setenv("ACDC_MCP_PORT", "not-a-number")

	_, err := LoadSettings()
	if err == nil {
		t.Fatal("Expected error for invalid port type")
	}
}

// TestLoadSettingsWithFlags_CLIOverridesEnv verifies that CLI flags take priority over env vars
func TestLoadSettingsWithFlags_CLIOverridesEnv(t *testing.T) {
	// Set env var
	t.Setenv("ACDC_MCP_PORT", "9090")
	t.Setenv("ACDC_MCP_TRANSPORT", "sse")

	// Create flags with different values
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Int("port", 0, "")
	flags.String("transport", "", "")
	_ = flags.Set("port", "7777")
	_ = flags.Set("transport", "stdio")

	settings, err := LoadSettingsWithFlags(flags)
	if err != nil {
		t.Fatalf("Failed to load settings: %v", err)
	}

	// CLI should win
	if settings.Port != 7777 {
		t.Errorf("Expected CLI port 7777, got %d", settings.Port)
	}
	if settings.Transport != "stdio" {
		t.Errorf("Expected CLI transport 'stdio', got '%s'", settings.Transport)
	}
}

// TestLoadSettingsWithFlags_EnvOverridesDefault verifies env vars override defaults
func TestLoadSettingsWithFlags_EnvOverridesDefault(t *testing.T) {
	t.Setenv("ACDC_MCP_HOST", "192.168.1.1")

	settings, err := LoadSettingsWithFlags(nil)
	if err != nil {
		t.Fatalf("Failed to load settings: %v", err)
	}

	// Default is 0.0.0.0, env should override
	if settings.Host != "192.168.1.1" {
		t.Errorf("Expected env host '192.168.1.1', got '%s'", settings.Host)
	}
}

// TestLoadSettingsWithFlags_NilFlags is same as LoadSettings behavior
func TestLoadSettingsWithFlags_NilFlags(t *testing.T) {
	_ = os.Unsetenv("ACDC_MCP_PORT")

	settings, err := LoadSettingsWithFlags(nil)
	if err != nil {
		t.Fatalf("Failed to load settings: %v", err)
	}

	// Should get default port
	if settings.Port != 8080 {
		t.Errorf("Expected default port 8080, got %d", settings.Port)
	}
}

// TestLoadSettingsWithFlags_AllFlagTypes verifies all flag types work
func TestLoadSettingsWithFlags_AllFlagTypes(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("content-dir", "", "")
	flags.String("transport", "", "")
	flags.String("host", "", "")
	flags.Int("port", 0, "")
	flags.Int("search-max-results", 0, "")
	flags.String("auth-type", "", "")
	flags.String("auth-basic-username", "", "")
	flags.String("auth-basic-password", "", "")
	flags.StringSlice("auth-api-keys", nil, "")

	_ = flags.Set("content-dir", "/custom/path")
	_ = flags.Set("transport", "stdio")
	_ = flags.Set("host", "localhost")
	_ = flags.Set("port", "3000")
	_ = flags.Set("search-max-results", "50")
	_ = flags.Set("auth-type", "basic")
	_ = flags.Set("auth-basic-username", "testuser")
	_ = flags.Set("auth-basic-password", "testpass")

	settings, err := LoadSettingsWithFlags(flags)
	if err != nil {
		t.Fatalf("Failed to load settings: %v", err)
	}

	if settings.ContentDir != "/custom/path" {
		t.Errorf("Expected content-dir '/custom/path', got '%s'", settings.ContentDir)
	}
	if settings.Transport != "stdio" {
		t.Errorf("Expected transport 'stdio', got '%s'", settings.Transport)
	}
	if settings.Host != "localhost" {
		t.Errorf("Expected host 'localhost', got '%s'", settings.Host)
	}
	if settings.Port != 3000 {
		t.Errorf("Expected port 3000, got %d", settings.Port)
	}
	if settings.Search.MaxResults != 50 {
		t.Errorf("Expected max results 50, got %d", settings.Search.MaxResults)
	}
	if settings.Auth.Type != "basic" {
		t.Errorf("Expected auth type 'basic', got '%s'", settings.Auth.Type)
	}
	if settings.Auth.Basic.Username != "testuser" {
		t.Errorf("Expected username 'testuser', got '%s'", settings.Auth.Basic.Username)
	}
	if settings.Auth.Basic.Password != "testpass" {
		t.Errorf("Expected password 'testpass', got '%s'", settings.Auth.Basic.Password)
	}
}

// --- ValidateSettings Tests ---

func TestValidateSettings_ValidNone(t *testing.T) {
	s := &Settings{Auth: AuthSettings{Type: AuthTypeNone}}
	if err := ValidateSettings(s); err != nil {
		t.Errorf("Expected no error for valid none auth, got: %v", err)
	}
}

func TestValidateSettings_ValidNone_EmptyType(t *testing.T) {
	s := &Settings{Auth: AuthSettings{Type: ""}}
	if err := ValidateSettings(s); err != nil {
		t.Errorf("Expected no error for empty auth type, got: %v", err)
	}
}

func TestValidateSettings_ValidBasic(t *testing.T) {
	s := &Settings{
		Auth: AuthSettings{
			Type: AuthTypeBasic,
			Basic: BasicAuthSettings{
				Username: "admin",
				Password: "secret",
			},
		},
	}
	if err := ValidateSettings(s); err != nil {
		t.Errorf("Expected no error for valid basic auth, got: %v", err)
	}
}

func TestValidateSettings_ValidAPIKey(t *testing.T) {
	s := &Settings{
		Auth: AuthSettings{
			Type:    AuthTypeAPIKey,
			APIKeys: []string{"key1", "key2"},
		},
	}
	if err := ValidateSettings(s); err != nil {
		t.Errorf("Expected no error for valid apikey auth, got: %v", err)
	}
}

func TestValidateSettings_NoneWithCredentials(t *testing.T) {
	tests := []struct {
		name     string
		settings Settings
	}{
		{
			name: "none with username",
			settings: Settings{
				Auth: AuthSettings{
					Type:  AuthTypeNone,
					Basic: BasicAuthSettings{Username: "admin"},
				},
			},
		},
		{
			name: "none with password",
			settings: Settings{
				Auth: AuthSettings{
					Type:  AuthTypeNone,
					Basic: BasicAuthSettings{Password: "secret"},
				},
			},
		},
		{
			name: "none with api keys",
			settings: Settings{
				Auth: AuthSettings{
					Type:    AuthTypeNone,
					APIKeys: []string{"key1"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSettings(&tt.settings)
			if err == nil {
				t.Fatal("Expected error for none with credentials")
			}
			if !strings.Contains(err.Error(), "incompatible") {
				t.Errorf("Expected 'incompatible' in error, got: %v", err)
			}
		})
	}
}

func TestValidateSettings_BasicAuthMissingUsername(t *testing.T) {
	s := &Settings{
		Auth: AuthSettings{
			Type: AuthTypeBasic,
			Basic: BasicAuthSettings{
				Password: "secret",
			},
		},
	}
	err := ValidateSettings(s)
	if err == nil {
		t.Fatal("Expected error for basic auth without username")
	}
	if !strings.Contains(err.Error(), "username and password") {
		t.Errorf("Expected 'username and password' in error, got: %v", err)
	}
}

func TestValidateSettings_BasicAuthMissingPassword(t *testing.T) {
	s := &Settings{
		Auth: AuthSettings{
			Type: AuthTypeBasic,
			Basic: BasicAuthSettings{
				Username: "admin",
			},
		},
	}
	err := ValidateSettings(s)
	if err == nil {
		t.Fatal("Expected error for basic auth without password")
	}
}

func TestValidateSettings_BasicAuthWithAPIKeys(t *testing.T) {
	s := &Settings{
		Auth: AuthSettings{
			Type: AuthTypeBasic,
			Basic: BasicAuthSettings{
				Username: "admin",
				Password: "secret",
			},
			APIKeys: []string{"key1"},
		},
	}
	err := ValidateSettings(s)
	if err == nil {
		t.Fatal("Expected error for basic + api keys")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("Expected 'mutually exclusive' in error, got: %v", err)
	}
}

func TestValidateSettings_APIKeyMissingKeys(t *testing.T) {
	s := &Settings{
		Auth: AuthSettings{
			Type: AuthTypeAPIKey,
		},
	}
	err := ValidateSettings(s)
	if err == nil {
		t.Fatal("Expected error for apikey without keys")
	}
	if !strings.Contains(err.Error(), "requires at least one") {
		t.Errorf("Expected 'requires at least one' in error, got: %v", err)
	}
}

func TestValidateSettings_APIKeyWithBasicCreds(t *testing.T) {
	s := &Settings{
		Auth: AuthSettings{
			Type:    AuthTypeAPIKey,
			APIKeys: []string{"key1"},
			Basic: BasicAuthSettings{
				Username: "admin",
			},
		},
	}
	err := ValidateSettings(s)
	if err == nil {
		t.Fatal("Expected error for apikey + basic creds")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("Expected 'mutually exclusive' in error, got: %v", err)
	}
}

func TestValidateSettings_UnknownAuthType(t *testing.T) {
	s := &Settings{
		Auth: AuthSettings{
			Type: "oauth",
		},
	}
	err := ValidateSettings(s)
	if err == nil {
		t.Fatal("Expected error for unknown auth type")
	}
	if !strings.Contains(err.Error(), "unknown auth-type") {
		t.Errorf("Expected 'unknown auth-type' in error, got: %v", err)
	}
}
