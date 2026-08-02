package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// schemeRegexp validates URI schemes per RFC 3986: ALPHA *( ALPHA / DIGIT / "+" / "-" / "." )
var schemeRegexp = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+\-.]*$`)

// SearchResultMode controls the detail level in search results.
type SearchResultMode string

const (
	SearchResultModeReferences SearchResultMode = "references"
	SearchResultModeContent    SearchResultMode = "content"
)

// Default relative weights for the five searchable fields. Exported so the
// viper defaults and ranking-sensitive tests cannot drift apart.
//
// A boost is a weight within a normalized disjunction, not a ranking override:
// raising one clause is bounded by Bleve's per-clause normalization. A boost of
// 0 removes its field from the query entirely.
const (
	DefaultKeywordsBoost float64 = 3.0
	DefaultTitleBoost    float64 = 2.0
	DefaultHeadingBoost  float64 = 2.5
	DefaultPathBoost     float64 = 1.25
	DefaultContentBoost  float64 = 1.0
)

// SearchSettings configuration for search service
type SearchSettings struct {
	MaxResults    int              `mapstructure:"max_results"`
	InMemory      bool             `mapstructure:"in_memory"`
	KeywordsBoost float64          `mapstructure:"keywords_boost"`
	HeadingBoost  float64          `mapstructure:"heading_boost"`
	TitleBoost    float64          `mapstructure:"title_boost"`
	PathBoost     float64          `mapstructure:"path_boost"`
	ContentBoost  float64          `mapstructure:"content_boost"`
	ResultMode    SearchResultMode `mapstructure:"result_mode"`
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
	Scheme     string         `mapstructure:"uri_scheme"`
	CrossRef   bool           `mapstructure:"cross_ref"`
	Search     SearchSettings `mapstructure:"search"`
	Auth       AuthSettings   `mapstructure:"auth"`
}

// LoadSettings loads settings from environment variables and optional .env file
func LoadSettings() (*Settings, error) {
	return LoadSettingsWithFlags(nil)
}

// LoadSettingsWithFlags loads settings with optional CLI flag overrides.
// Priority: CLI flags > environment variables > .env file > defaults.
// If flags is nil, only env vars and defaults are used.
func LoadSettingsWithFlags(flags *pflag.FlagSet) (*Settings, error) {
	v := viper.New()

	// Default values
	cwd, _ := os.Getwd()

	v.SetDefault("content_dir", cwd)
	v.SetDefault("transport", "stdio")
	v.SetDefault("host", "0.0.0.0")
	v.SetDefault("port", 8080)
	v.SetDefault("uri_scheme", "acdc")
	v.SetDefault("search.max_results", 10)
	v.SetDefault("search.keywords_boost", DefaultKeywordsBoost)
	v.SetDefault("search.heading_boost", DefaultHeadingBoost)
	v.SetDefault("search.title_boost", DefaultTitleBoost)
	v.SetDefault("search.path_boost", DefaultPathBoost)
	v.SetDefault("search.content_boost", DefaultContentBoost)
	v.SetDefault("search.result_mode", SearchResultModeReferences)
	v.SetDefault("cross_ref", false)
	v.SetDefault("auth.type", AuthTypeNone)

	// Environment variables
	v.SetEnvPrefix("ACDC_MCP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Bind specific env vars for nested config.
	// BindEnv only returns an error if the key is empty, which cannot happen
	// with hardcoded keys. Errors are intentionally discarded here.
	_ = v.BindEnv("search.max_results", "ACDC_MCP_SEARCH_MAX_RESULTS")
	_ = v.BindEnv("search.keywords_boost", "ACDC_MCP_SEARCH_KEYWORDS_BOOST")
	_ = v.BindEnv("search.heading_boost", "ACDC_MCP_SEARCH_HEADING_BOOST")
	_ = v.BindEnv("search.path_boost", "ACDC_MCP_SEARCH_PATH_BOOST")
	_ = v.BindEnv("search.title_boost", "ACDC_MCP_SEARCH_TITLE_BOOST")
	_ = v.BindEnv("search.content_boost", "ACDC_MCP_SEARCH_CONTENT_BOOST")
	_ = v.BindEnv("search.result_mode", "ACDC_MCP_SEARCH_RESULT_MODE")

	_ = v.BindEnv("uri_scheme", "ACDC_MCP_URI_SCHEME")
	_ = v.BindEnv("cross_ref", "ACDC_MCP_CROSS_REF")

	_ = v.BindEnv("auth.type", "ACDC_MCP_AUTH_TYPE")
	_ = v.BindEnv("auth.basic.username", "ACDC_MCP_AUTH_BASIC_USERNAME")
	_ = v.BindEnv("auth.basic.password", "ACDC_MCP_AUTH_BASIC_PASSWORD")
	_ = v.BindEnv("auth.api_keys", "ACDC_MCP_AUTH_API_KEYS")

	// Bind CLI flags if provided (highest priority)
	if flags != nil {
		_ = v.BindPFlag("content_dir", flags.Lookup("content-dir"))
		_ = v.BindPFlag("transport", flags.Lookup("transport"))
		_ = v.BindPFlag("host", flags.Lookup("host"))
		_ = v.BindPFlag("port", flags.Lookup("port"))
		_ = v.BindPFlag("uri_scheme", flags.Lookup("uri-scheme"))
		_ = v.BindPFlag("cross_ref", flags.Lookup("cross-ref"))
		_ = v.BindPFlag("search.max_results", flags.Lookup("search-max-results"))
		_ = v.BindPFlag("search.keywords_boost", flags.Lookup("search-keywords-boost"))
		_ = v.BindPFlag("search.heading_boost", flags.Lookup("search-heading-boost"))
		_ = v.BindPFlag("search.path_boost", flags.Lookup("search-path-boost"))
		_ = v.BindPFlag("search.title_boost", flags.Lookup("search-title-boost"))
		_ = v.BindPFlag("search.content_boost", flags.Lookup("search-content-boost"))
		_ = v.BindPFlag("search.result_mode", flags.Lookup("search-result-mode"))
		_ = v.BindPFlag("auth.type", flags.Lookup("auth-type"))
		_ = v.BindPFlag("auth.basic.username", flags.Lookup("auth-basic-username"))
		_ = v.BindPFlag("auth.basic.password", flags.Lookup("auth-basic-password"))
		_ = v.BindPFlag("auth.api_keys", flags.Lookup("auth-api-keys"))
	}

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

// ValidateSettings checks for conflicting configurations.
// Returns an error if the settings contain mutually exclusive or incomplete auth config.
func ValidateSettings(s *Settings) error {
	// Validate transport type
	switch s.Transport {
	case "stdio", "sse":
		// valid
	default:
		return errors.New("transport must be 'stdio' or 'sse', got: " + s.Transport)
	}

	// Validate URI scheme (RFC 3986: ALPHA *( ALPHA / DIGIT / "+" / "-" / "." ))
	if !schemeRegexp.MatchString(s.Scheme) {
		return errors.New("scheme must match RFC 3986 (start with a letter, contain only letters, digits, +, -, .), got: " + s.Scheme)
	}

	switch s.Search.ResultMode {
	case SearchResultModeReferences, SearchResultModeContent:
	default:
		return errors.New("search result mode must be 'references' or 'content', got: " + string(s.Search.ResultMode))
	}

	if err := validateBoosts(s.Search); err != nil {
		return err
	}

	hasBasicCreds := s.Auth.Basic.Username != "" || s.Auth.Basic.Password != ""
	hasAPIKeys := len(s.Auth.APIKeys) > 0

	switch s.Auth.Type {
	case AuthTypeNone, "":
		if hasBasicCreds || hasAPIKeys {
			return errors.New("auth-type 'none' is incompatible with auth credentials")
		}
	case AuthTypeBasic:
		if hasAPIKeys {
			return errors.New("auth-type 'basic' is mutually exclusive with auth-api-keys")
		}
		if s.Auth.Basic.Username == "" || s.Auth.Basic.Password == "" {
			return errors.New("auth-type 'basic' requires both username and password")
		}
	case AuthTypeAPIKey:
		if hasBasicCreds {
			return errors.New("auth-type 'apikey' is mutually exclusive with basic auth credentials")
		}
		if !hasAPIKeys {
			return errors.New("auth-type 'apikey' requires at least one API key")
		}
	default:
		return errors.New("unknown auth-type: " + s.Auth.Type)
	}

	return nil
}

// validateBoosts rejects boost values Bleve cannot score meaningfully. Zero is
// legal and disables the field's clause.
func validateBoosts(s SearchSettings) error {
	boosts := []struct {
		key   string
		value float64
	}{
		{"search.keywords_boost", s.KeywordsBoost},
		{"search.heading_boost", s.HeadingBoost},
		{"search.title_boost", s.TitleBoost},
		{"search.path_boost", s.PathBoost},
		{"search.content_boost", s.ContentBoost},
	}
	for _, boost := range boosts {
		if math.IsNaN(boost.value) || math.IsInf(boost.value, 0) || boost.value < 0 {
			return fmt.Errorf("%s must be a finite, non-negative number, got: %v", boost.key, boost.value)
		}
	}
	return nil
}
