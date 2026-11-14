package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

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

const (
	MaxUIResultLimit    = 100
	DefaultUIMaxResults = 20
)

// UISettings configuration for the browser-based web UI
type UISettings struct {
	Enabled    bool `mapstructure:"enabled"`
	MaxResults int  `mapstructure:"max_results"`
}

// ServerMode selects how a stdio launch obtains its server. The set of values
// is open to extension without a contract change.
type ServerMode string

const (
	ServerModeShared  ServerMode = "shared"
	ServerModeSession ServerMode = "session"
)

const (
	DefaultSharedServerConnectWait = 5 * time.Second
	DefaultSharedServerLinger      = 30 * time.Second
	MinSharedServerLinger          = time.Second
)

type SharedServerSettings struct {
	ConnectWait time.Duration `mapstructure:"connect_wait"`
	Linger      time.Duration `mapstructure:"linger"`
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
	UI         UISettings     `mapstructure:"ui"`

	ServerMode   ServerMode           `mapstructure:"server_mode"`
	SharedServer SharedServerSettings `mapstructure:"shared_server"`
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
	v.SetDefault("search.in_memory", true)
	v.SetDefault("cross_ref", false)
	v.SetDefault("auth.type", AuthTypeNone)
	v.SetDefault("ui.enabled", false)
	v.SetDefault("ui.max_results", DefaultUIMaxResults)
	v.SetDefault("server_mode", ServerModeShared)
	v.SetDefault("shared_server.connect_wait", DefaultSharedServerConnectWait)
	v.SetDefault("shared_server.linger", DefaultSharedServerLinger)

	// Environment variables
	v.SetEnvPrefix("ACDC_MCP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Bind specific env vars for nested config.
	// BindEnv only returns an error if the key is empty, which cannot happen
	// with hardcoded keys. Errors are intentionally discarded here.
	_ = v.BindEnv("search.max_results", "ACDC_MCP_SEARCH_MAX_RESULTS")
	_ = v.BindEnv("search.in_memory", "ACDC_MCP_SEARCH_IN_MEMORY")
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

	_ = v.BindEnv("ui.enabled", "ACDC_MCP_UI_ENABLED")
	_ = v.BindEnv("ui.max_results", "ACDC_MCP_UI_MAX_RESULTS")

	_ = v.BindEnv("server_mode", "ACDC_MCP_SERVER_MODE")
	_ = v.BindEnv("shared_server.connect_wait", "ACDC_MCP_SHARED_SERVER_CONNECT_WAIT")
	_ = v.BindEnv("shared_server.linger", "ACDC_MCP_SHARED_SERVER_LINGER")

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
		_ = v.BindPFlag("search.in_memory", flags.Lookup("search-in-memory"))
		_ = v.BindPFlag("auth.type", flags.Lookup("auth-type"))
		_ = v.BindPFlag("auth.basic.username", flags.Lookup("auth-basic-username"))
		_ = v.BindPFlag("auth.basic.password", flags.Lookup("auth-basic-password"))
		_ = v.BindPFlag("auth.api_keys", flags.Lookup("auth-api-keys"))
		_ = v.BindPFlag("ui.enabled", flags.Lookup("ui-enabled"))
		_ = v.BindPFlag("ui.max_results", flags.Lookup("ui-max-results"))
		_ = v.BindPFlag("server_mode", flags.Lookup("server-mode"))
		_ = v.BindPFlag("shared_server.connect_wait", flags.Lookup("shared-server-connect-wait"))
		_ = v.BindPFlag("shared_server.linger", flags.Lookup("shared-server-linger"))
	}

	// Helper to look for .env file
	v.SetConfigName(".env")
	v.SetConfigType("env")
	v.AddConfigPath(".")
	_ = v.ReadInConfig() // Ignore error if .env doesn't exist

	if err := checkDurationFormat(v, "shared_server.connect_wait", "shared_server.linger"); err != nil {
		return nil, err
	}
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
	if err := validateTransport(s.Transport); err != nil {
		return err
	}
	if err := validateScheme(s.Scheme); err != nil {
		return err
	}
	if err := validateSearch(s.Search); err != nil {
		return err
	}
	if err := validateUI(s.UI); err != nil {
		return err
	}
	if err := validateServerMode(s.ServerMode, s.SharedServer); err != nil {
		return err
	}

	return validateAuth(s.Auth)
}

// validateUI ignores a disabled UI so an unused, out-of-range ui.max_results
// cannot block startup.
func validateUI(ui UISettings) error {
	if !ui.Enabled {
		return nil
	}
	if ui.MaxResults < 1 || ui.MaxResults > MaxUIResultLimit {
		return fmt.Errorf("ui.max_results must be between 1 and %d, got: %d", MaxUIResultLimit, ui.MaxResults)
	}

	return nil
}

// validateServerMode applies to every launch, including those where the
// settings have no effect, so a value is never valid in one context only.
func validateServerMode(mode ServerMode, shared SharedServerSettings) error {
	switch mode {
	case ServerModeShared, ServerModeSession:
	default:
		return fmt.Errorf("server_mode must be '%s' or '%s', got: %s", ServerModeShared, ServerModeSession, mode)
	}
	if shared.ConnectWait <= 0 {
		return fmt.Errorf("shared_server.connect_wait must be greater than 0, got: %s", shared.ConnectWait)
	}
	if shared.Linger < MinSharedServerLinger {
		return fmt.Errorf("shared_server.linger must be at least %s, got: %s", MinSharedServerLinger, shared.Linger)
	}

	return nil
}

func validateTransport(transport string) error {
	switch transport {
	case "stdio", "sse":
		return nil
	default:
		return errors.New("transport must be 'stdio' or 'sse', got: " + transport)
	}
}

// validateScheme enforces RFC 3986: ALPHA *( ALPHA / DIGIT / "+" / "-" / "." ).
func validateScheme(scheme string) error {
	if !schemeRegexp.MatchString(scheme) {
		return errors.New("scheme must match RFC 3986 (start with a letter, contain only letters, digits, +, -, .), got: " + scheme)
	}

	return nil
}

func validateSearch(s SearchSettings) error {
	switch s.ResultMode {
	case SearchResultModeReferences, SearchResultModeContent:
	default:
		return errors.New("search result mode must be 'references' or 'content', got: " + string(s.ResultMode))
	}

	return validateBoosts(s)
}

func validateAuth(a AuthSettings) error {
	hasBasicCreds := a.Basic.Username != "" || a.Basic.Password != ""
	hasAPIKeys := len(a.APIKeys) > 0

	switch a.Type {
	case AuthTypeNone, "":
		if hasBasicCreds || hasAPIKeys {
			return errors.New("auth-type 'none' is incompatible with auth credentials")
		}
		return nil
	case AuthTypeBasic:
		return validateBasicAuth(a.Basic, hasAPIKeys)
	case AuthTypeAPIKey:
		return validateAPIKeyAuth(hasBasicCreds, hasAPIKeys)
	default:
		return errors.New("unknown auth-type: " + a.Type)
	}
}

func validateBasicAuth(basic BasicAuthSettings, hasAPIKeys bool) error {
	if hasAPIKeys {
		return errors.New("auth-type 'basic' is mutually exclusive with auth-api-keys")
	}
	if basic.Username == "" || basic.Password == "" {
		return errors.New("auth-type 'basic' requires both username and password")
	}

	return nil
}

func validateAPIKeyAuth(hasBasicCreds, hasAPIKeys bool) error {
	if hasBasicCreds {
		return errors.New("auth-type 'apikey' is mutually exclusive with basic auth credentials")
	}
	if !hasAPIKeys {
		return errors.New("auth-type 'apikey' requires at least one API key")
	}

	return nil
}

// maxBoost is a conservative sanity cap on search boosts, not a guarantee
// that scoring stays sound below it. Bleve's term weight squares boost × idf,
// not the boost alone, so a value well under this cap can already collapse
// every score to zero on a corpus whose idf is large enough. The true
// overflow threshold depends on corpus statistics and clause count, neither
// known at configuration-validation time, so this cap only rejects
// magnitudes no meaningful ranking could ever use.
var maxBoost = math.Sqrt(math.MaxFloat64)

// validateBoosts rejects boost values Bleve cannot score meaningfully. An
// individual zero is legal and disables that field's clause; all five at zero
// is not, because the resulting clauseless disjunction answers every query
// with nothing while reporting no error.
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
	anyPositive := false
	for _, boost := range boosts {
		if math.IsNaN(boost.value) || math.IsInf(boost.value, 0) || boost.value < 0 {
			return fmt.Errorf("%s must be a finite, non-negative number, got: %v", boost.key, boost.value)
		}
		if boost.value > maxBoost {
			return fmt.Errorf("%s must not exceed %v, a sanity cap on search boosts, got: %v", boost.key, maxBoost, boost.value)
		}
		if boost.value > 0 {
			anyPositive = true
		}
	}
	if !anyPositive {
		return errors.New("at least one of search.keywords_boost, search.heading_boost, search.title_boost, search.path_boost, or search.content_boost must be positive; all five at zero disables search entirely")
	}
	return nil
}

// checkDurationFormat runs before Unmarshal so a malformed value, notably a
// bare number, names the expected format instead of surfacing only the
// decoder's time.ParseDuration error.
func checkDurationFormat(v *viper.Viper, keys ...string) error {
	for _, key := range keys {
		raw := v.GetString(key)
		if !isGoDuration(raw) {
			return fmt.Errorf("%s must be a Go duration such as 500ms, 5s or 1m, got: %q", key, raw)
		}
	}
	return nil
}

// isGoDuration rejects every unitless number, including the 0, +0 and -0 that
// time.ParseDuration accepts, so a bare number never reaches range validation.
func isGoDuration(raw string) bool {
	if _, err := strconv.ParseFloat(raw, 64); err == nil {
		return false
	}
	_, err := time.ParseDuration(raw)
	return err == nil
}
