package identity_test

import (
	"os"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

// clearACDCEnv blanks every ACDC_MCP_* variable for the test; the loader
// treats an empty variable as unset.
func clearACDCEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "ACDC_MCP_") {
			t.Setenv(name, "")
		}
	}
}

// loaderFlags declares the subset of the CLI flags this test drives, under the
// names the loader binds.
func loaderFlags(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	flags := pflag.NewFlagSet("identity-test", pflag.ContinueOnError)
	flags.String("content-dir", "", "")
	flags.String("transport", "", "")
	flags.Int("port", 0, "")
	flags.Int("search-max-results", 0, "")
	flags.Bool("search-in-memory", true, "")
	flags.String("search-result-mode", "", "")
	flags.String("uri-scheme", "", "")
	flags.String("server-mode", "", "")
	require.NoError(t, flags.Parse(args))
	return flags
}

func loadIdentity(t *testing.T, flags *pflag.FlagSet) string {
	t.Helper()
	s, err := config.LoadSettingsWithFlags(flags)
	require.NoError(t, err)
	require.NoError(t, config.ValidateSettings(s))
	id, err := identity.Compute(s, identity.Binary{Version: "1.2.3", Build: "abc123"})
	require.NoError(t, err)
	return id
}

func TestCompute_SameEffectiveSettingsFromAnySourceGiveSameIdentity(t *testing.T) {
	clearACDCEnv(t)
	contentDir := t.TempDir()
	t.Chdir(contentDir)

	defaults := loadIdentity(t, nil)

	sources := map[string]func(t *testing.T) *pflag.FlagSet{
		"defaults with empty flag set": func(t *testing.T) *pflag.FlagSet { return loaderFlags(t) },
		"variables": func(t *testing.T) *pflag.FlagSet {
			t.Setenv("ACDC_MCP_CONTENT_DIR", contentDir)
			t.Setenv("ACDC_MCP_SEARCH_MAX_RESULTS", "10")
			t.Setenv("ACDC_MCP_SEARCH_IN_MEMORY", "true")
			t.Setenv("ACDC_MCP_SEARCH_RESULT_MODE", "references")
			t.Setenv("ACDC_MCP_URI_SCHEME", "acdc")
			return nil
		},
		"flags": func(t *testing.T) *pflag.FlagSet {
			return loaderFlags(t,
				"--content-dir", contentDir,
				"--search-max-results", "10",
				"--search-in-memory=true",
				"--search-result-mode", "references",
				"--uri-scheme", "acdc",
			)
		},
		"flags in another order": func(t *testing.T) *pflag.FlagSet {
			return loaderFlags(t,
				"--uri-scheme", "acdc",
				"--search-result-mode", "references",
				"--search-in-memory=true",
				"--search-max-results", "10",
				"--content-dir", contentDir,
			)
		},
		"relative content dir flag": func(t *testing.T) *pflag.FlagSet {
			return loaderFlags(t, "--content-dir", ".")
		},
		"flag overriding a variable": func(t *testing.T) *pflag.FlagSet {
			t.Setenv("ACDC_MCP_SEARCH_MAX_RESULTS", "42")
			return loaderFlags(t, "--search-max-results", "10")
		},
		"excluded settings from mixed sources": func(t *testing.T) *pflag.FlagSet {
			t.Setenv("ACDC_MCP_HOST", "127.0.0.1")
			t.Setenv("ACDC_MCP_AUTH_TYPE", "apikey")
			t.Setenv("ACDC_MCP_AUTH_API_KEYS", "k1,k2")
			t.Setenv("ACDC_MCP_SHARED_SERVER_LINGER", "1m")
			return loaderFlags(t, "--port", "9999", "--transport", "sse", "--server-mode", "session")
		},
	}
	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, defaults, loadIdentity(t, source(t)))
		})
	}
}

func TestCompute_DifferentEffectiveSettingsFromLoaderGiveDifferentIdentity(t *testing.T) {
	clearACDCEnv(t)
	t.Chdir(t.TempDir())
	defaults := loadIdentity(t, nil)

	t.Setenv("ACDC_MCP_SEARCH_IN_MEMORY", "false")
	fromVariable := loadIdentity(t, nil)
	require.NotEqual(t, defaults, fromVariable)

	t.Setenv("ACDC_MCP_SEARCH_IN_MEMORY", "")
	require.Equal(t, fromVariable, loadIdentity(t, loaderFlags(t, "--search-in-memory=false")))
}
