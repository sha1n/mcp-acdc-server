package config

import (
	"log/slog"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestLoadSettings_Defaults(t *testing.T) {
	// Clear env vars to ensure defaults are used
	unsetEnvForTest(t,
		"ACDC_MCP_PORT",
		"ACDC_MCP_AUTH_TYPE",
		"ACDC_MCP_URI_SCHEME",
		"ACDC_MCP_SEARCH_IN_MEMORY",
	)

	settings, err := LoadSettings()
	if err != nil {
		t.Fatalf("Failed to load settings: %v", err)
	}

	if settings.Port != 8080 {
		t.Errorf("Expected default port 8080, got %d", settings.Port)
	}
	if settings.Scheme != "acdc" {
		t.Errorf("Expected default scheme 'acdc', got '%s'", settings.Scheme)
	}
	if settings.Auth.Type != AuthTypeNone {
		t.Errorf("Expected default auth type '%s', got '%s'", AuthTypeNone, settings.Auth.Type)
	}
	if settings.Search.KeywordsBoost != 3.0 {
		t.Errorf("Expected default keywords boost 3.0, got %f", settings.Search.KeywordsBoost)
	}
	if settings.Search.TitleBoost != 2.0 {
		t.Errorf("Expected default title boost 2.0, got %f", settings.Search.TitleBoost)
	}
	if settings.Search.ContentBoost != 1.0 {
		t.Errorf("Expected default content boost 1.0, got %f", settings.Search.ContentBoost)
	}
	if settings.CrossRef != false {
		t.Errorf("Expected default cross_ref false, got %v", settings.CrossRef)
	}
	if !settings.Search.InMemory {
		t.Error("Expected search.in_memory to default to true")
	}
}

func TestLoadSettings_SearchResultMode(t *testing.T) {
	t.Setenv("ACDC_MCP_SEARCH_RESULT_MODE", "content")
	settings, err := LoadSettings()
	require.NoError(t, err)
	require.Equal(t, SearchResultModeContent, settings.Search.ResultMode)
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

	t.Setenv("ACDC_MCP_SEARCH_KEYWORDS_BOOST", "5.5")
	settings, _ = LoadSettings()
	if settings.Search.KeywordsBoost != 5.5 {
		t.Errorf("Expected keywords boost 5.5, got %f", settings.Search.KeywordsBoost)
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
	// Environment variables outrank a config file in viper, so the keys under
	// assertion have to be absent for the file to be observable at all.
	unsetEnvForTest(t, "ACDC_MCP_HOST", "ACDC_MCP_PORT")

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
	unsetEnvForTest(t, "ACDC_MCP_PORT")

	settings, err := LoadSettingsWithFlags(nil)
	if err != nil {
		t.Fatalf("Failed to load settings: %v", err)
	}

	// Should get default port
	if settings.Port != 8080 {
		t.Errorf("Expected default port 8080, got %d", settings.Port)
	}
}

// TestLoadSettings_ContentDirDefaultsToWorkingDirectory pins the zero-config
// default: with no flag and no env var, content_dir is the process working
// directory itself, not a "content" subdirectory beneath it.
func TestLoadSettings_ContentDirDefaultsToWorkingDirectory(t *testing.T) {
	unsetEnvForTest(t, "ACDC_MCP_CONTENT_DIR")

	wd, err := os.Getwd()
	require.NoError(t, err)

	settings, err := LoadSettings()
	require.NoError(t, err)
	require.Equal(t, wd, settings.ContentDir)
}

// TestLoadSettingsWithFlags_ContentDirFlagOverridesDefault verifies an
// explicit --content-dir flag still wins over the working-directory default.
func TestLoadSettingsWithFlags_ContentDirFlagOverridesDefault(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("content-dir", "", "")
	require.NoError(t, flags.Set("content-dir", "/custom/path"))

	settings, err := LoadSettingsWithFlags(flags)
	require.NoError(t, err)
	require.Equal(t, "/custom/path", settings.ContentDir)
}

// TestLoadSettings_ContentDirEnvVarOverridesDefault verifies
// ACDC_MCP_CONTENT_DIR still wins over the working-directory default.
func TestLoadSettings_ContentDirEnvVarOverridesDefault(t *testing.T) {
	t.Setenv("ACDC_MCP_CONTENT_DIR", "/env/path")

	settings, err := LoadSettings()
	require.NoError(t, err)
	require.Equal(t, "/env/path", settings.ContentDir)
}

// TestLoadSettingsWithFlags_AllFlagTypes verifies all flag types work
func TestLoadSettingsWithFlags_AllFlagTypes(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("content-dir", "", "")
	flags.String("transport", "", "")
	flags.String("host", "", "")
	flags.Int("port", 0, "")
	flags.Int("search-max-results", 0, "")
	flags.Float64("search-keywords-boost", 0, "")
	flags.Float64("search-title-boost", 0, "")
	flags.Float64("search-content-boost", 0, "")
	flags.String("auth-type", "", "")
	flags.String("auth-basic-username", "", "")
	flags.String("auth-basic-password", "", "")
	flags.StringSlice("auth-api-keys", nil, "")

	_ = flags.Set("content-dir", "/custom/path")
	_ = flags.Set("transport", "stdio")
	_ = flags.Set("host", "localhost")
	_ = flags.Set("port", "3000")
	_ = flags.Set("search-max-results", "50")
	_ = flags.Set("search-keywords-boost", "10.0")
	_ = flags.Set("search-title-boost", "5.0")
	_ = flags.Set("search-content-boost", "2.0")
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
	if settings.Search.KeywordsBoost != 10.0 {
		t.Errorf("Expected keywords boost 10.0, got %f", settings.Search.KeywordsBoost)
	}
	if settings.Search.TitleBoost != 5.0 {
		t.Errorf("Expected title boost 5.0, got %f", settings.Search.TitleBoost)
	}
	if settings.Search.ContentBoost != 2.0 {
		t.Errorf("Expected content boost 2.0, got %f", settings.Search.ContentBoost)
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

// TestLoadSettingsWithFlags_SearchResultModeDefaultsToReferences pins the
// process-wide default result mode when --search-result-mode is registered
// but left unset, exercising the same flag-unset fallback path CLI users hit
// when they don't pass the flag (internal/app/cli.go registers it with an
// empty-string default so viper falls through to this SetDefault).
func TestLoadSettingsWithFlags_SearchResultModeDefaultsToReferences(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("search-result-mode", "", "")

	settings, err := LoadSettingsWithFlags(flags)
	require.NoError(t, err)
	require.Equal(t, SearchResultModeReferences, settings.Search.ResultMode)
}

// --- ValidateSettings Tests ---

// defaultSearchSettings returns a SearchSettings populated with the default
// boosts and the default result mode. ValidateSettings rejects an all-zero
// boost configuration, so tests exercising unrelated validation rules — auth,
// transport, scheme — can no longer rely on the Go zero value here and start
// from this instead, mutating only the field under test.
func defaultSearchSettings() SearchSettings {
	return SearchSettings{
		ResultMode:    SearchResultModeReferences,
		KeywordsBoost: DefaultKeywordsBoost,
		HeadingBoost:  DefaultHeadingBoost,
		TitleBoost:    DefaultTitleBoost,
		PathBoost:     DefaultPathBoost,
		ContentBoost:  DefaultContentBoost,
		SemanticFloor: DefaultSemanticFloor,
	}
}

func TestValidateSettings_ValidNone(t *testing.T) {
	s := &Settings{Transport: "stdio", Scheme: "acdc", Search: defaultSearchSettings(), Auth: AuthSettings{Type: AuthTypeNone}}
	if err := ValidateSettings(s); err != nil {
		t.Errorf("Expected no error for valid none auth, got: %v", err)
	}
}

func TestValidateSettings_SearchResultMode(t *testing.T) {
	settings := &Settings{
		Transport: "stdio",
		Scheme:    "acdc",
		Search:    defaultSearchSettings(),
		Auth:      AuthSettings{Type: AuthTypeNone},
	}
	settings.Search.ResultMode = "verbose"
	require.EqualError(t, ValidateSettings(settings), "search result mode must be 'references' or 'content', got: verbose")
}

func TestValidateSettings_ValidNone_EmptyType(t *testing.T) {
	s := &Settings{Transport: "stdio", Scheme: "acdc", Search: defaultSearchSettings(), Auth: AuthSettings{Type: ""}}
	if err := ValidateSettings(s); err != nil {
		t.Errorf("Expected no error for empty auth type, got: %v", err)
	}
}

func TestValidateSettings_ValidBasic(t *testing.T) {
	s := &Settings{
		Transport: "stdio",
		Scheme:    "acdc",
		Search:    defaultSearchSettings(),
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
		Transport: "stdio",
		Scheme:    "acdc",
		Search:    defaultSearchSettings(),
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
				Transport: "stdio",
				Scheme:    "acdc",
				Search:    defaultSearchSettings(),
				Auth: AuthSettings{
					Type:  AuthTypeNone,
					Basic: BasicAuthSettings{Username: "admin"},
				},
			},
		},
		{
			name: "none with password",
			settings: Settings{
				Transport: "stdio",
				Scheme:    "acdc",
				Search:    defaultSearchSettings(),
				Auth: AuthSettings{
					Type:  AuthTypeNone,
					Basic: BasicAuthSettings{Password: "secret"},
				},
			},
		},
		{
			name: "none with api keys",
			settings: Settings{
				Transport: "stdio",
				Scheme:    "acdc",
				Search:    defaultSearchSettings(),
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
		Transport: "stdio",
		Scheme:    "acdc",
		Search:    defaultSearchSettings(),
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
		Transport: "stdio",
		Scheme:    "acdc",
		Search:    defaultSearchSettings(),
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
		Transport: "stdio",
		Scheme:    "acdc",
		Search:    defaultSearchSettings(),
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
		Transport: "stdio",
		Scheme:    "acdc",
		Search:    defaultSearchSettings(),
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
		Transport: "stdio",
		Scheme:    "acdc",
		Search:    defaultSearchSettings(),
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
		Transport: "stdio",
		Scheme:    "acdc",
		Search:    defaultSearchSettings(),
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

// --- Transport Validation Tests ---

func TestValidateSettings_ValidTransportStdio(t *testing.T) {
	s := &Settings{Transport: "stdio", Scheme: "acdc", Search: defaultSearchSettings(), Auth: AuthSettings{Type: AuthTypeNone}}
	if err := ValidateSettings(s); err != nil {
		t.Errorf("Expected no error for valid stdio transport, got: %v", err)
	}
}

func TestValidateSettings_ValidTransportSSE(t *testing.T) {
	s := &Settings{Transport: "sse", Scheme: "acdc", Search: defaultSearchSettings(), Auth: AuthSettings{Type: AuthTypeNone}}
	if err := ValidateSettings(s); err != nil {
		t.Errorf("Expected no error for valid sse transport, got: %v", err)
	}
}

func TestValidateSettings_InvalidTransport(t *testing.T) {
	tests := []struct {
		name      string
		transport string
	}{
		{"empty transport", ""},
		{"http transport", "http"},
		{"websocket transport", "websocket"},
		{"unknown transport", "foobar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Settings{
				Transport: tt.transport,
				Scheme:    "acdc",
				Search:    defaultSearchSettings(),
				Auth:      AuthSettings{Type: AuthTypeNone},
			}
			err := ValidateSettings(s)
			if err == nil {
				t.Fatalf("Expected error for transport %q", tt.transport)
			}
			if !strings.Contains(err.Error(), "transport must be") {
				t.Errorf("Expected 'transport must be' in error, got: %v", err)
			}
		})
	}
}

// --- Cross-Ref Tests ---

func TestLoadSettings_CrossRefEnvVar(t *testing.T) {
	t.Setenv("ACDC_MCP_CROSS_REF", "true")

	settings, err := LoadSettings()
	if err != nil {
		t.Fatalf("Failed to load settings: %v", err)
	}

	if !settings.CrossRef {
		t.Errorf("Expected cross_ref true, got %v", settings.CrossRef)
	}
}

func TestLoadSettingsWithFlags_CrossRefCLIOverridesEnv(t *testing.T) {
	t.Setenv("ACDC_MCP_CROSS_REF", "false")

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Bool("cross-ref", false, "")
	_ = flags.Set("cross-ref", "true")

	settings, err := LoadSettingsWithFlags(flags)
	if err != nil {
		t.Fatalf("Failed to load settings: %v", err)
	}

	if !settings.CrossRef {
		t.Errorf("Expected cross_ref true from CLI flag, got %v", settings.CrossRef)
	}
}

// --- Scheme Tests ---

func TestLoadSettings_SchemeEnvVar(t *testing.T) {
	t.Setenv("ACDC_MCP_URI_SCHEME", "custom")

	settings, err := LoadSettings()
	if err != nil {
		t.Fatalf("Failed to load settings: %v", err)
	}

	if settings.Scheme != "custom" {
		t.Errorf("Expected scheme 'custom', got '%s'", settings.Scheme)
	}
}

func TestLoadSettingsWithFlags_SchemeCLIOverridesEnv(t *testing.T) {
	t.Setenv("ACDC_MCP_URI_SCHEME", "from-env")

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("uri-scheme", "", "")
	_ = flags.Set("uri-scheme", "from-cli")

	settings, err := LoadSettingsWithFlags(flags)
	if err != nil {
		t.Fatalf("Failed to load settings: %v", err)
	}

	if settings.Scheme != "from-cli" {
		t.Errorf("Expected scheme 'from-cli', got '%s'", settings.Scheme)
	}
}

func TestValidateSettings_ValidSchemes(t *testing.T) {
	tests := []struct {
		name   string
		scheme string
	}{
		{"default acdc", "acdc"},
		{"with hyphen", "my-scheme"},
		{"with dot and plus", "x.y+z"},
		{"uppercase start", "A1"},
		{"single letter", "x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Settings{Transport: "stdio", Scheme: tt.scheme, Search: defaultSearchSettings(), Auth: AuthSettings{Type: AuthTypeNone}}
			if err := ValidateSettings(s); err != nil {
				t.Errorf("Expected no error for scheme %q, got: %v", tt.scheme, err)
			}
		})
	}
}

func TestValidateSettings_InvalidSchemes(t *testing.T) {
	tests := []struct {
		name   string
		scheme string
	}{
		{"empty", ""},
		{"starts with digit", "123"},
		{"contains space", "a b"},
		{"colon-slash-slash", "://"},
		{"contains colon-slash", "a://b"},
		{"contains bang", "a!b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Settings{Transport: "stdio", Scheme: tt.scheme, Search: defaultSearchSettings(), Auth: AuthSettings{Type: AuthTypeNone}}
			err := ValidateSettings(s)
			if err == nil {
				t.Fatalf("Expected error for scheme %q", tt.scheme)
			}
			if !strings.Contains(err.Error(), "scheme must match") {
				t.Errorf("Expected 'scheme must match' in error, got: %v", err)
			}
		})
	}
}

func TestLoadSettingsWithFlags_SearchTitleBoostFlag(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Float64("search-title-boost", 0, "")
	require.NoError(t, flags.Set("search-title-boost", "5.0"))

	settings, err := LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.Equal(t, 5.0, settings.Search.TitleBoost)
}

func TestLoadSettings_SearchTitleBoostEnvVar(t *testing.T) {
	t.Setenv("ACDC_MCP_SEARCH_TITLE_BOOST", "4.5")

	settings, err := LoadSettings()

	require.NoError(t, err)
	require.Equal(t, 4.5, settings.Search.TitleBoost)
}

// The pre-rename env var is no longer a supported input.
func TestLoadSettings_SearchTitleBoostIgnoresRetiredNameEnvVar(t *testing.T) {
	t.Setenv("ACDC_MCP_SEARCH_NAME_BOOST", "9.0")

	settings, err := LoadSettings()

	require.NoError(t, err)
	require.Equal(t, 2.0, settings.Search.TitleBoost)
}

func TestLoadSettings_SearchHeadingBoostDefault(t *testing.T) {
	settings, err := LoadSettings()

	require.NoError(t, err)
	require.Equal(t, 2.5, settings.Search.HeadingBoost)
}

func TestLoadSettings_SearchPathBoostDefault(t *testing.T) {
	settings, err := LoadSettings()

	require.NoError(t, err)
	require.Equal(t, 1.25, settings.Search.PathBoost)
}

func TestLoadSettings_SearchHeadingBoostEnvVar(t *testing.T) {
	t.Setenv("ACDC_MCP_SEARCH_HEADING_BOOST", "4.5")

	settings, err := LoadSettings()

	require.NoError(t, err)
	require.Equal(t, 4.5, settings.Search.HeadingBoost)
}

func TestLoadSettings_SearchPathBoostEnvVar(t *testing.T) {
	t.Setenv("ACDC_MCP_SEARCH_PATH_BOOST", "0.5")

	settings, err := LoadSettings()

	require.NoError(t, err)
	require.Equal(t, 0.5, settings.Search.PathBoost)
}

func TestLoadSettingsWithFlags_SearchHeadingBoostFlagBeatsEnvVar(t *testing.T) {
	t.Setenv("ACDC_MCP_SEARCH_HEADING_BOOST", "4.5")
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Float64("search-heading-boost", 0, "")
	require.NoError(t, flags.Set("search-heading-boost", "6.0"))

	settings, err := LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.Equal(t, 6.0, settings.Search.HeadingBoost)
}

func TestLoadSettingsWithFlags_SearchPathBoostFlagBeatsEnvVar(t *testing.T) {
	t.Setenv("ACDC_MCP_SEARCH_PATH_BOOST", "0.5")
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Float64("search-path-boost", 0, "")
	require.NoError(t, flags.Set("search-path-boost", "3.0"))

	settings, err := LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.Equal(t, 3.0, settings.Search.PathBoost)
}

// The exported defaults and the values viper installs must not drift apart.
func TestLoadSettings_BoostDefaultsMatchExportedConstants(t *testing.T) {
	settings, err := LoadSettings()

	require.NoError(t, err)
	require.Equal(t, DefaultKeywordsBoost, settings.Search.KeywordsBoost)
	require.Equal(t, DefaultTitleBoost, settings.Search.TitleBoost)
	require.Equal(t, DefaultHeadingBoost, settings.Search.HeadingBoost)
	require.Equal(t, DefaultPathBoost, settings.Search.PathBoost)
	require.Equal(t, DefaultContentBoost, settings.Search.ContentBoost)
}

func TestValidateSettings_RejectsInvalidBoosts(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*SearchSettings)
		wantErr string
	}{
		{"negative keywords", func(s *SearchSettings) { s.KeywordsBoost = -1 }, "search.keywords_boost"},
		{"negative heading", func(s *SearchSettings) { s.HeadingBoost = -0.5 }, "search.heading_boost"},
		{"negative title", func(s *SearchSettings) { s.TitleBoost = -1 }, "search.title_boost"},
		{"negative path", func(s *SearchSettings) { s.PathBoost = -1 }, "search.path_boost"},
		{"negative content", func(s *SearchSettings) { s.ContentBoost = -1 }, "search.content_boost"},
		{"NaN heading", func(s *SearchSettings) { s.HeadingBoost = math.NaN() }, "search.heading_boost"},
		{"positive infinity path", func(s *SearchSettings) { s.PathBoost = math.Inf(1) }, "search.path_boost"},
		{"negative infinity content", func(s *SearchSettings) { s.ContentBoost = math.Inf(-1) }, "search.content_boost"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			search := SearchSettings{ResultMode: SearchResultModeReferences}
			tt.mutate(&search)
			s := &Settings{Transport: "stdio", Scheme: "acdc", Search: search, Auth: AuthSettings{Type: AuthTypeNone}}

			err := ValidateSettings(s)

			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// Zero disables a field's contribution and is a supported operator choice —
// notably --search-path-boost 0. An individual zero must never be rejected.
func TestValidateSettings_AcceptsIndividualZeroBoosts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SearchSettings)
	}{
		{"zero keywords", func(s *SearchSettings) { s.KeywordsBoost = 0 }},
		{"zero heading", func(s *SearchSettings) { s.HeadingBoost = 0 }},
		{"zero title", func(s *SearchSettings) { s.TitleBoost = 0 }},
		{"zero path", func(s *SearchSettings) { s.PathBoost = 0 }},
		{"zero content", func(s *SearchSettings) { s.ContentBoost = 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			search := defaultSearchSettings()
			tt.mutate(&search)
			s := &Settings{Transport: "stdio", Scheme: "acdc", Search: search, Auth: AuthSettings{Type: AuthTypeNone}}

			require.NoError(t, ValidateSettings(s))
		})
	}
}

// All five boosts at zero produces a clauseless disjunction, which bleve
// answers with MatchNoneSearcher: every real query returns nothing while "*"
// still returns everything. Silent, and indistinguishable from an empty index.
func TestValidateSettings_RejectsAllZeroBoosts(t *testing.T) {
	s := &Settings{
		Transport: "stdio",
		Scheme:    "acdc",
		Search: SearchSettings{
			ResultMode:    SearchResultModeReferences,
			KeywordsBoost: 0, HeadingBoost: 0, TitleBoost: 0, PathBoost: 0, ContentBoost: 0,
		},
		Auth: AuthSettings{Type: AuthTypeNone},
	}

	err := ValidateSettings(s)

	require.Error(t, err)
	require.Contains(t, err.Error(), "at least one")
}

// Above sqrt(MaxFloat64) the squared weight overflows to +Inf inside bleve's
// sumOfSquaredWeights, queryNorm collapses to zero, every score becomes
// exactly zero, and ranking degenerates to the sort tiebreak. Such a value is
// finite and non-negative, so the pre-existing checks let it through.
func TestValidateSettings_RejectsBoostsBleveCannotSquare(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*SearchSettings)
		wantErr string
	}{
		{"overflowing keywords", func(s *SearchSettings) { s.KeywordsBoost = 1e200 }, "search.keywords_boost"},
		{"overflowing heading", func(s *SearchSettings) { s.HeadingBoost = 1e200 }, "search.heading_boost"},
		{"largest float content", func(s *SearchSettings) { s.ContentBoost = math.MaxFloat64 }, "search.content_boost"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			search := defaultSearchSettings()
			tt.mutate(&search)
			s := &Settings{Transport: "stdio", Scheme: "acdc", Search: search, Auth: AuthSettings{Type: AuthTypeNone}}

			err := ValidateSettings(s)

			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// The bound exists to stop overflow, not to cap how far an operator may push a
// field. A large value bleve can still square stays legal.
func TestValidateSettings_AcceptsLargeButSquarableBoosts(t *testing.T) {
	search := defaultSearchSettings()
	search.HeadingBoost = 1e150
	s := &Settings{
		Transport: "stdio",
		Scheme:    "acdc",
		Search:    search,
		Auth:      AuthSettings{Type: AuthTypeNone},
	}

	require.NoError(t, ValidateSettings(s))
}

// The check is strict (>), so the cap itself is a legal boost value. Only
// values strictly greater than maxBoost are rejected.
func TestValidateSettings_AcceptsBoostAtExactCap(t *testing.T) {
	search := defaultSearchSettings()
	search.HeadingBoost = maxBoost
	s := &Settings{
		Transport: "stdio",
		Scheme:    "acdc",
		Search:    search,
		Auth:      AuthSettings{Type: AuthTypeNone},
	}

	require.NoError(t, ValidateSettings(s))
}

// A malformed boost fails to decode rather than coercing to zero, which would
// silently disable the field. Pinned because validateBoosts cannot catch a
// value that has already become a legal 0.
func TestLoadSettings_MalformedBoostIsRejected(t *testing.T) {
	t.Setenv("ACDC_MCP_SEARCH_HEADING_BOOST", "2.5x")

	settings, err := LoadSettings()

	require.Error(t, err)
	require.Nil(t, settings)
	require.Contains(t, err.Error(), "search.heading_boost")
}

// TestLoadSettings_InMemoryEnvVarOverridesDefault covers both directions
// deliberately. search.in_memory has no CLI flag, so this env var is the only
// supported way to choose the index kind, and it must work whichever way the
// default points.
func TestLoadSettings_InMemoryEnvVarOverridesDefault(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{value: "true", expected: true},
		{value: "false", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Setenv("ACDC_MCP_SEARCH_IN_MEMORY", tt.value)

			settings, err := LoadSettings()
			require.NoError(t, err)
			require.Equal(t, tt.expected, settings.Search.InMemory)
		})
	}
}

// unsetEnvForTest clears the named environment variables for the duration of the
// test and restores each one that was set beforehand. testing.T offers Setenv but
// no counterpart that clears, so the capture and restore have to be explicit.
func unsetEnvForTest(t testing.TB, keys ...string) {
	t.Helper()

	for _, key := range keys {
		prev, had := os.LookupEnv(key)
		_ = os.Unsetenv(key)
		if had {
			t.Cleanup(func() { _ = os.Setenv(key, prev) })
		}
	}
}

// TestUnsetEnvForTest_RestoresOnlyPreviouslySetVariables pins both halves of the
// contract: a variable that was set comes back with its original value, and one
// that was absent stays absent rather than reappearing as an empty string, which
// LoadSettings cannot distinguish from unset for every key.
func TestUnsetEnvForTest_RestoresOnlyPreviouslySetVariables(t *testing.T) {
	const key = "ACDC_MCP_TEST_UNSET_HELPER"
	const value = "original"

	tests := []struct {
		name       string
		setUpfront bool
	}{
		{name: "restores a variable that was set", setUpfront: true},
		{name: "leaves a variable that was unset alone", setUpfront: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv establishes a known baseline and undoes itself, so the
			// arrangement never depends on the helper under test.
			t.Setenv(key, value)
			if !tt.setUpfront {
				require.NoError(t, os.Unsetenv(key))
			}

			cleared := false
			t.Run("under the helper", func(t *testing.T) {
				unsetEnvForTest(t, key)

				_, present := os.LookupEnv(key)
				require.False(t, present, "helper must clear the variable")
				cleared = true
			})
			require.True(t, cleared, "the subtest asserting the cleared state must have run")

			// Subtest cleanups have run by the time t.Run returns.
			restored, present := os.LookupEnv(key)
			require.Equal(t, tt.setUpfront, present)
			if tt.setUpfront {
				require.Equal(t, value, restored)
			}
		})
	}
}

func TestLoadSettings_SemanticModelDefaultsToOff(t *testing.T) {
	settings, err := LoadSettings()
	require.NoError(t, err)
	require.Empty(t, settings.Search.SemanticModel, "semantic search is off unless an operator configures a model")
}

func TestLoadSettings_SemanticModelFromEnv(t *testing.T) {
	t.Setenv("ACDC_MCP_SEARCH_SEMANTIC_MODEL", "/models/potion-base-8m")

	settings, err := LoadSettings()

	require.NoError(t, err)
	require.Equal(t, "/models/potion-base-8m", settings.Search.SemanticModel)
}

func TestLoadSettingsWithFlags_SemanticModelFlagBeatsEnv(t *testing.T) {
	t.Setenv("ACDC_MCP_SEARCH_SEMANTIC_MODEL", "/models/from-env")

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("search-semantic-model", "", "")
	require.NoError(t, flags.Parse([]string{"--search-semantic-model", "/models/from-flag"}))

	settings, err := LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.Equal(t, "/models/from-flag", settings.Search.SemanticModel)
}

func TestLoadSettings_SemanticFloorDefaultsToThePlaceholder(t *testing.T) {
	settings, err := LoadSettings()
	require.NoError(t, err)
	require.Equal(t, DefaultSemanticFloor, settings.Search.SemanticFloor)
}

func TestLoadSettings_SemanticFloorFromEnv(t *testing.T) {
	t.Setenv("ACDC_MCP_SEARCH_SEMANTIC_FLOOR", "0.42")

	settings, err := LoadSettings()

	require.NoError(t, err)
	require.InDelta(t, 0.42, settings.Search.SemanticFloor, 1e-9)
}

func TestValidateSettings_SemanticFloorRange(t *testing.T) {
	tests := []struct {
		name    string
		floor   float64
		wantErr bool
	}{
		{name: "the default", floor: DefaultSemanticFloor},
		{name: "disabled", floor: -1},
		{name: "exact matches only", floor: 1},
		{name: "above the cosine range", floor: 1.5, wantErr: true},
		{name: "below the cosine range", floor: -1.5, wantErr: true},
		{name: "not a number", floor: math.NaN(), wantErr: true},
		{name: "infinite", floor: math.Inf(1), wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := &Settings{
				Transport: "stdio",
				Scheme:    "acdc",
				Search:    defaultSearchSettings(),
				Auth:      AuthSettings{Type: AuthTypeNone},
			}
			settings.Search.SemanticFloor = test.floor

			err := ValidateSettings(settings)

			if test.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "search.semantic_floor")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// A path that does not load is the factory's error to report, so validation
// must not reject one here — including a path that does not exist.
func TestValidateSettings_AcceptsAnySemanticModelPath(t *testing.T) {
	settings := &Settings{
		Transport: "stdio",
		Scheme:    "acdc",
		Search:    defaultSearchSettings(),
		Auth:      AuthSettings{Type: AuthTypeNone},
	}
	settings.Search.SemanticModel = "/definitely/not/a/real/path"

	require.NoError(t, ValidateSettings(settings))
}

func TestLoadSettings_LogLevelDefaultsToInfo(t *testing.T) {
	unsetEnvForTest(t, "ACDC_MCP_LOG_LEVEL")

	settings, err := LoadSettings()
	require.NoError(t, err)
	require.Equal(t, "info", settings.LogLevel)
}

func TestLoadSettings_LogLevelFromEnv(t *testing.T) {
	unsetEnvForTest(t, "ACDC_MCP_LOG_LEVEL")
	t.Setenv("ACDC_MCP_LOG_LEVEL", "debug")

	settings, err := LoadSettings()
	require.NoError(t, err)
	require.Equal(t, "debug", settings.LogLevel)
}

func TestLoadSettingsWithFlags_LogLevelFlagOverridesEnv(t *testing.T) {
	unsetEnvForTest(t, "ACDC_MCP_LOG_LEVEL")
	t.Setenv("ACDC_MCP_LOG_LEVEL", "error")

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("log-level", "", "")
	require.NoError(t, flags.Set("log-level", "warn"))

	settings, err := LoadSettingsWithFlags(flags)
	require.NoError(t, err)
	require.Equal(t, "warn", settings.LogLevel)
}

func TestValidateSettings_LogLevel(t *testing.T) {
	tests := []struct {
		level   string
		wantErr bool
	}{
		{"debug", false},
		{"info", false},
		{"warn", false},
		{"error", false},
		{"DEBUG", false},
		{"", false},
		{"trace", true},
	}

	for _, tc := range tests {
		t.Run(tc.level, func(t *testing.T) {
			settings := &Settings{
				Transport: "stdio",
				Scheme:    "acdc",
				LogLevel:  tc.level,
				Search: SearchSettings{
					ResultMode:   SearchResultModeReferences,
					ContentBoost: 1.0,
				},
				Auth: AuthSettings{Type: AuthTypeNone},
			}

			err := ValidateSettings(settings)
			if tc.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "log level")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		level string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"Warn", slog.LevelWarn},
	}

	for _, tc := range tests {
		t.Run(tc.level, func(t *testing.T) {
			got, err := ParseLogLevel(tc.level)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestParseLogLevel_EmptyResolvesToDefault(t *testing.T) {
	got, err := ParseLogLevel("")
	require.NoError(t, err)
	require.Equal(t, slog.LevelInfo, got)
}

func TestParseLogLevel_Unknown(t *testing.T) {
	_, err := ParseLogLevel("trace")
	require.Error(t, err)
	require.Contains(t, err.Error(), "log level")
}
