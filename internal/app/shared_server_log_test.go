package app

import (
	"bytes"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var sharedServerLogTestTime = regexp.MustCompile(`^time=\S+ `)

func sharedServerLogTestSettings(auth config.AuthSettings) *config.Settings {
	return &config.Settings{
		ContentDir: "/content",
		Transport:  "stdio",
		Host:       "0.0.0.0",
		Port:       8080,
		Search: config.SearchSettings{
			MaxResults: 10, InMemory: true, KeywordsBoost: 3, HeadingBoost: 2.5,
			PathBoost: 2, TitleBoost: 2, ContentBoost: 1, ResultMode: "references",
		},
		Auth:         auth,
		UI:           config.UISettings{Enabled: true, MaxResults: 20},
		ServerMode:   config.ServerModeShared,
		SharedServer: config.SharedServerSettings{ConnectWait: 5 * time.Second, Linger: time.Minute},
	}
}

func sharedServerLogTestLines(t *testing.T, buf *bytes.Buffer) []string {
	t.Helper()
	var lines []string
	for line := range strings.SplitSeq(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		require.Regexp(t, sharedServerLogTestTime, line)
		lines = append(lines, sharedServerLogTestTime.ReplaceAllString(line, ""))
	}
	return lines
}

func TestLogSharedServerStartup_OmitsEveryIdentityExcludedSetting(t *testing.T) {
	excluded := []string{"transport", "host", "port", "server_mode", "shared_server.", "auth", "ui.", "Web UI"}
	auths := map[string]config.AuthSettings{
		"basic":  {Type: config.AuthTypeBasic, Basic: config.BasicAuthSettings{Username: "secret-user", Password: "secret-pass"}},
		"apikey": {Type: config.AuthTypeAPIKey, APIKeys: []string{"k1", "k2"}},
		"none":   {Type: "none"},
	}

	for name, auth := range auths {
		t.Run(name, func(t *testing.T) {
			settings := sharedServerLogTestSettings(auth)
			var full, shared bytes.Buffer
			config.LogWithLogger(settings, slog.New(slog.NewTextHandler(&full, nil)))

			logSharedServerStartup(slog.New(slog.NewTextHandler(&shared, nil)), settings, "1.2.3")

			sharedLines := sharedServerLogTestLines(t, &shared)
			require.NotEmpty(t, sharedLines)
			assert.Equal(t,
				`level=INFO msg="Starting MCP Acdc shared server" version=1.2.3 pid=`+strconv.Itoa(os.Getpid()),
				sharedLines[0], "the startup entry carries the PID")

			var wantDump []string
			for _, line := range sharedServerLogTestLines(t, &full) {
				if !sharedServerLogTestMentionsAny(line, excluded) {
					wantDump = append(wantDump, line)
				}
			}
			assert.Equal(t, wantDump, sharedLines[1:], "the dump is today's dump minus the excluded settings, in the same format")
			assert.Contains(t, sharedLines[1:], `level=INFO msg="Config: content_dir" value=/content`)
			for _, line := range sharedLines {
				assert.False(t, sharedServerLogTestMentionsAny(line, excluded), "excluded setting logged: %s", line)
				assert.NotContains(t, line, "secret-user")
				assert.NotContains(t, line, "count=")
			}
		})
	}
}

func sharedServerLogTestMentionsAny(line string, words []string) bool {
	msg := line[strings.Index(line, "msg="):]
	for _, w := range words {
		if strings.Contains(msg, "Config: "+w) || strings.Contains(msg, w+" requires") {
			return true
		}
	}
	return false
}

func TestLogSharedServerStartup_OutputIsByteIdentical(t *testing.T) {
	settings := sharedServerLogTestSettings(config.AuthSettings{Type: config.AuthTypeAPIKey, APIKeys: []string{"k1"}})
	var buf bytes.Buffer

	logSharedServerStartup(slog.New(slog.NewTextHandler(&buf, nil)), settings, "1.2.3")

	assert.Equal(t, []string{
		`level=INFO msg="Starting MCP Acdc shared server" version=1.2.3 pid=` + strconv.Itoa(os.Getpid()),
		`level=INFO msg="Config: content_dir" value=/content`,
		`level=INFO msg="Config: search.max_results" value=10`,
		`level=INFO msg="Config: search.in_memory" value=true`,
		`level=INFO msg="Config: search.keywords_boost" value=3`,
		`level=INFO msg="Config: search.heading_boost" value=2.5`,
		`level=INFO msg="Config: search.path_boost" value=2`,
		`level=INFO msg="Config: search.title_boost" value=2`,
		`level=INFO msg="Config: search.content_boost" value=1`,
		`level=INFO msg="Config: search.result_mode" value=references`,
	}, sharedServerLogTestLines(t, &buf))
}

var sharedServerLogTestConfigKey = regexp.MustCompile(`msg="Config: ([^"]+)"`)

// The agreement with the identity is checked here because identity imports
// config, so the config package's own tests cannot call identity.Compute.
func TestLogIdentitySettings_AgreesWithServerIdentity(t *testing.T) {
	variants := map[string]func(*config.Settings){
		"sse with basic auth": func(s *config.Settings) {
			s.Transport = "sse"
			s.Auth = config.AuthSettings{Type: config.AuthTypeBasic, Basic: config.BasicAuthSettings{Username: "u", Password: "p"}}
		},
		"stdio with api keys and ignored web UI": func(s *config.Settings) {
			s.Auth = config.AuthSettings{Type: config.AuthTypeAPIKey, APIKeys: []string{"k1"}}
		},
		"stdio without auth or web UI": func(s *config.Settings) {
			s.Auth = config.AuthSettings{Type: config.AuthTypeNone}
			s.UI.Enabled = false
		},
	}
	bin := identity.Binary{Version: "1.2.3", Build: "abc"}

	for name, variant := range variants {
		t.Run(name, func(t *testing.T) {
			contentDir := t.TempDir()
			settings := func() *config.Settings {
				s := sharedServerLogTestSettings(config.AuthSettings{})
				s.ContentDir = contentDir
				variant(s)
				return s
			}
			var full, shared bytes.Buffer
			config.LogWithLogger(settings(), slog.New(slog.NewTextHandler(&full, nil)))
			config.LogIdentitySettings(settings(), slog.New(slog.NewTextHandler(&shared, nil)))
			fullLines := sharedServerLogTestLines(t, &full)
			sharedLines := sharedServerLogTestLines(t, &shared)
			baseID, err := identity.Compute(settings(), bin)
			require.NoError(t, err)

			var derived []string
			for _, line := range fullLines {
				inShared := slices.Contains(sharedLines, line)
				if inShared {
					derived = append(derived, line)
				}
				match := sharedServerLogTestConfigKey.FindStringSubmatch(line)
				if match == nil {
					assert.False(t, inShared, "a line naming no setting reached the shared dump: %s", line)
					continue
				}
				perturbed := settings()
				sharedServerLogTestPerturb(t, perturbed, match[1])
				id, err := identity.Compute(perturbed, bin)
				require.NoError(t, err)
				assert.Equal(t, id != baseID, inShared, "classification of %q disagrees with the identity", match[1])
			}
			assert.Equal(t, derived, sharedLines, "the shared dump is the per-session dump restricted to identity-relevant lines")
		})
	}
}

func sharedServerLogTestPerturb(t *testing.T, s *config.Settings, key string) {
	t.Helper()
	v := reflect.ValueOf(s).Elem()
	for part := range strings.SplitSeq(key, ".") {
		v = sharedServerLogTestField(t, v, part, key)
	}
	switch v.Kind() {
	case reflect.String:
		if key == "content_dir" {
			v.SetString(t.TempDir())
			return
		}
		v.SetString(v.String() + "x")
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int, reflect.Int64:
		v.SetInt(v.Int() + 1)
	case reflect.Float64:
		v.SetFloat(v.Float() + 1)
	case reflect.Slice:
		v.Set(reflect.Append(v, reflect.Zero(v.Type().Elem())))
	default:
		t.Fatalf("setting %s has kind %s, which this test cannot change", key, v.Kind())
	}
}

func sharedServerLogTestField(t *testing.T, v reflect.Value, name, key string) reflect.Value {
	t.Helper()
	require.Equal(t, reflect.Struct, v.Kind(), "setting %s is not a settings field", key)
	for i := range v.NumField() {
		if v.Type().Field(i).Tag.Get("mapstructure") == name {
			return v.Field(i)
		}
	}
	t.Fatalf("dump line %q names no settings field", key)
	return reflect.Value{}
}
