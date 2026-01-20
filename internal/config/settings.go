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

// AuthSettings configuration for authentication
type AuthSettings struct {
	Type   string            `mapstructure:"type"` // "none", "basic", "oidc", "apikey"
	Basic  BasicAuthSettings `mapstructure:"basic"`
	OIDC   OIDCSettings      `mapstructure:"oidc"`
	APIKey string            `mapstructure:"api_key"`
}

// BasicAuthSettings configuration for basic auth
type BasicAuthSettings struct {
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
}

// OIDCSettings configuration for OIDC
type OIDCSettings struct {
	IssuerURL string `mapstructure:"issuer_url"`
	ClientID  string `mapstructure:"client_id"`
}

// Settings application settings
type Settings struct {
	ContentDir string         `mapstructure:"content_dir"`
	Transport  string         `mapstructure:"transport"`
	Host       string         `mapstructure:"host"`
	Port       int            `mapstructure:"port"`
	CertFile   string         `mapstructure:"cert_file"`
	KeyFile    string         `mapstructure:"key_file"`
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
	v.SetDefault("auth.type", "none")

	// Environment variables
	v.SetEnvPrefix("ACDC_MCP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	_ = v.BindEnv("cert_file", "ACDC_MCP_CERT_FILE")
	_ = v.BindEnv("key_file", "ACDC_MCP_KEY_FILE")

	// Bind specific env vars for nested config
	_ = v.BindEnv("search.max_results", "ACDC_MCP_SEARCH_MAX_RESULTS")
	_ = v.BindEnv("search.heap_size_mb", "ACDC_MCP_SEARCH_HEAP_SIZE_MB")

	_ = v.BindEnv("auth.type", "ACDC_MCP_AUTH_TYPE")
	_ = v.BindEnv("auth.basic.username", "ACDC_MCP_AUTH_BASIC_USERNAME")
	_ = v.BindEnv("auth.basic.password", "ACDC_MCP_AUTH_BASIC_PASSWORD")
	_ = v.BindEnv("auth.oidc.issuer_url", "ACDC_MCP_AUTH_OIDC_ISSUER_URL")
	_ = v.BindEnv("auth.oidc.client_id", "ACDC_MCP_AUTH_OIDC_CLIENT_ID")
	_ = v.BindEnv("auth.api_key", "ACDC_MCP_AUTH_API_KEY")

	// Helper to look for .env file
	v.SetConfigName(".env")
	v.SetConfigType("env")
	v.AddConfigPath(".")
	_ = v.ReadInConfig() // Ignore error if .env doesn't exist

	var settings Settings
	if err := v.Unmarshal(&settings); err != nil {
		return nil, err
	}

	return &settings, nil
}
