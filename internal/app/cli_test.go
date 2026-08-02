package app

import (
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestRegisterFlags_AllFlagsExist(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)

	expectedFlags := []struct {
		long  string
		short string
	}{
		{"content-dir", "c"},
		{"transport", "t"},
		{"host", "H"},
		{"port", "p"},
		{"search-max-results", "m"},
		{"search-result-mode", ""},
		{"auth-type", "a"},
		{"auth-basic-username", "u"},
		{"auth-basic-password", "P"},
		{"auth-api-keys", "k"},
	}

	for _, ef := range expectedFlags {
		f := flags.Lookup(ef.long)
		if f == nil {
			t.Errorf("Flag --%s not registered", ef.long)
			continue
		}
		if f.Shorthand != ef.short {
			t.Errorf("Flag --%s has wrong shorthand: expected -%s, got -%s", ef.long, ef.short, f.Shorthand)
		}
	}
}

func TestCLI_FlagParsing_SearchResultMode(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NoError(t, flags.Set("search-result-mode", "content"))
	settings, err := config.LoadSettingsWithFlags(flags)
	require.NoError(t, err)
	require.Equal(t, config.SearchResultModeContent, settings.Search.ResultMode)
}

func TestRegisterFlags_FlagDescriptions(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)

	// Verify all flags have non-empty descriptions
	flags.VisitAll(func(f *pflag.Flag) {
		if f.Usage == "" {
			t.Errorf("Flag --%s has empty description", f.Name)
		}
	})
}

func TestCLI_FlagParsing_ContentDir(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)

	err := flags.Parse([]string{"--content-dir=/custom/path"})
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	val, err := flags.GetString("content-dir")
	if err != nil {
		t.Fatalf("GetString failed: %v", err)
	}
	if val != "/custom/path" {
		t.Errorf("Expected '/custom/path', got '%s'", val)
	}
}

func TestCLI_FlagParsing_ShortFlags(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)

	err := flags.Parse([]string{"-t", "stdio", "-p", "9000", "-a", "basic"})
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	transport, _ := flags.GetString("transport")
	port, _ := flags.GetInt("port")
	authType, _ := flags.GetString("auth-type")

	if transport != "stdio" {
		t.Errorf("Expected transport 'stdio', got '%s'", transport)
	}
	if port != 9000 {
		t.Errorf("Expected port 9000, got %d", port)
	}
	if authType != "basic" {
		t.Errorf("Expected auth type 'basic', got '%s'", authType)
	}
}

func TestCLI_FlagParsing_AuthAPIKeys(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)

	err := flags.Parse([]string{"--auth-api-keys=key1,key2,key3"})
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	keys, err := flags.GetStringSlice("auth-api-keys")
	if err != nil {
		t.Fatalf("GetStringSlice failed: %v", err)
	}

	if len(keys) != 3 {
		t.Fatalf("Expected 3 keys, got %d", len(keys))
	}
	if keys[0] != "key1" || keys[1] != "key2" || keys[2] != "key3" {
		t.Errorf("Unexpected keys: %v", keys)
	}
}

func TestRegisterFlags_SearchTitleBoostReplacesNameBoost(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)

	require.NotNil(t, flags.Lookup("search-title-boost"))
	require.Nil(t, flags.Lookup("search-name-boost"))
}

func TestCLI_FlagParsing_SearchTitleBoost(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NoError(t, flags.Set("search-title-boost", "7.5"))

	settings, err := config.LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.Equal(t, 7.5, settings.Search.TitleBoost)
}

func TestCLI_FlagParsing_SearchHeadingBoost(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NoError(t, flags.Set("search-heading-boost", "7.5"))

	settings, err := config.LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.Equal(t, 7.5, settings.Search.HeadingBoost)
}

func TestCLI_FlagParsing_SearchPathBoost(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NoError(t, flags.Set("search-path-boost", "7.5"))

	settings, err := config.LoadSettingsWithFlags(flags)

	require.NoError(t, err)
	require.Equal(t, 7.5, settings.Search.PathBoost)
}
