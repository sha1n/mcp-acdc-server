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

// Settings application settings
type Settings struct {
	ContentDir string         `mapstructure:"content_dir"`
	Transport  string         `mapstructure:"transport"`
	Host       string         `mapstructure:"host"`
	Port       int            `mapstructure:"port"`
	Search     SearchSettings `mapstructure:"search"`
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

	// Environment variables
	v.SetEnvPrefix("ACDC_MCP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Bind specific env vars for nested config
	_ = v.BindEnv("search.max_results", "ACDC_MCP_SEARCH_MAX_RESULTS")
	_ = v.BindEnv("search.heap_size_mb", "ACDC_MCP_SEARCH_HEAP_SIZE_MB")

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
