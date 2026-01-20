package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// SearchSettings configuration for search service
type SearchSettings struct {
	MaxResults int  `mapstructure:"max_results"`
	HeapSizeMB int  `mapstructure:"heap_size_mb"`
	InMemory   bool `mapstructure:"in_memory"`
}

// Auth type constants
const (
	AuthTypeNone   = "none"
	AuthTypeBasic  = "basic"
	AuthTypeAPIKey = "apikey"
)

// AuthSettings configuration for authentication
type AuthSettings struct {
	Type    string            `mapstructure:"type"` // AuthTypeNone, AuthTypeBasic, or AuthTypeAPIKey
	Basic   BasicAuthSettings `mapstructure:"basic"`
	APIKeys []string          `mapstructure:"api_keys"`
}

// BasicAuthSettings configuration for basic auth
type BasicAuthSettings struct {
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
}

// Settings application settings
type Settings struct {
	ContentDir string         `mapstructure:"content_dir"`
	Transport  string         `mapstructure:"transport"`
	Host       string         `mapstructure:"host"`
	Port       int            `mapstructure:"port"`
	Search     SearchSettings `mapstructure:"search"`
	Auth       AuthSettings   `mapstructure:"auth"`
}

// LoadSettings loads settings from environment variables and optional .env file
func LoadSettings() (*Settings, error) {
	v := viper.New()

	// Default values
	cwd, _ := os.Getwd()
	defaultContentDir := filepath.Join(cwd, "content")

	v.SetDefault("content_dir", defaultContentDir)
	v.SetDefault("transport", "sse")
	v.SetDefault("host", "0.0.0.0")
	v.SetDefault("port", 8000)
	v.SetDefault("search.max_results", 10)
	v.SetDefault("search.heap_size_mb", 50)
	v.SetDefault("auth.type", AuthTypeNone)

	// Environment variables
	v.SetEnvPrefix("ACDC_MCP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Bind specific env vars for nested config.
	// BindEnv only returns an error if the key is empty, which cannot happen
	// with hardcoded keys. Errors are intentionally discarded here.
	_ = v.BindEnv("search.max_results", "ACDC_MCP_SEARCH_MAX_RESULTS")
	_ = v.BindEnv("search.heap_size_mb", "ACDC_MCP_SEARCH_HEAP_SIZE_MB")

	_ = v.BindEnv("auth.type", "ACDC_MCP_AUTH_TYPE")
	_ = v.BindEnv("auth.basic.username", "ACDC_MCP_AUTH_BASIC_USERNAME")
	_ = v.BindEnv("auth.basic.password", "ACDC_MCP_AUTH_BASIC_PASSWORD")
	_ = v.BindEnv("auth.api_keys", "ACDC_MCP_AUTH_API_KEYS")

	// Helper to look for .env file
	v.SetConfigName(".env")
	v.SetConfigType("env")
	v.AddConfigPath(".")
	_ = v.ReadInConfig() // Ignore error if .env doesn't exist

	var settings Settings
	if err := v.Unmarshal(&settings); err != nil {
		return nil, err
	}

	// Handle explicit parsing of API keys if provided via env var as comma-separated string
	// Viper might return a single element slice containing the commas if it fails to split.
	// We explicitly fix this up.
	apiKeysEnv := os.Getenv("ACDC_MCP_AUTH_API_KEYS")
	if apiKeysEnv != "" {
		// If the struct is empty OR looks like a failed split (1 element with commas)
		if len(settings.Auth.APIKeys) == 0 || (len(settings.Auth.APIKeys) == 1 && strings.Contains(settings.Auth.APIKeys[0], ",")) {
			settings.Auth.APIKeys = strings.Split(apiKeysEnv, ",")
		}
	}

	// Always trim spaces from API keys (Viper might leave spaces after commas)
	for i := range settings.Auth.APIKeys {
		settings.Auth.APIKeys[i] = strings.TrimSpace(settings.Auth.APIKeys[i])
	}

	return &settings, nil
}
