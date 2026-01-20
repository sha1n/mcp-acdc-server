package config

import (
	"os"
	"testing"
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

	if settings.Port != 8000 {
		t.Errorf("Expected default port 8000, got %d", settings.Port)
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
