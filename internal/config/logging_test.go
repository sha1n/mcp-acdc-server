package config

import (
	"bytes"
	"context"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLog(t *testing.T) {
	tests := []struct {
		name     string
		settings *Settings
		expected []string
	}{
		{
			name: "stdio transport",
			settings: &Settings{
				ContentDir: "/tmp",
				Transport:  "stdio",
				Search: SearchSettings{
					MaxResults:    10,
					InMemory:      true,
					KeywordsBoost: 1.0,
					TitleBoost:    1.0,
					ContentBoost:  1.0,
					ResultMode:    SearchResultModeReferences,
				},
				Auth: AuthSettings{
					Type: AuthTypeNone,
				},
			},
			expected: []string{
				"Config: content_dir",
				"Config: transport",
				"Config: search.max_results",
				"Config: search.in_memory",
				"Config: search.result_mode",
				"Config: auth.type",
			},
		},
		{
			name: "sse transport",
			settings: &Settings{
				Transport: "sse",
				Host:      "1.2.3.4",
				Port:      9090,
				Auth: AuthSettings{
					Type: AuthTypeNone,
				},
			},
			expected: []string{
				"Config: host",
				"Config: port",
			},
		},
		{
			name: "basic auth",
			settings: &Settings{
				ContentDir: "/tmp",
				Transport:  "stdio",
				Search:     SearchSettings{},
				Auth: AuthSettings{
					Type: AuthTypeBasic,
					Basic: BasicAuthSettings{
						Username: "user",
						Password: "pass",
					},
				},
			},
			expected: []string{
				"Config: auth.basic.username",
				"Config: auth.basic.password",
			},
		},
		{
			name: "apikey auth",
			settings: &Settings{
				Auth: AuthSettings{
					Type:    AuthTypeAPIKey,
					APIKeys: []string{"k1", "k2"},
				},
			},
			expected: []string{
				"Config: auth.api_keys",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			captured := make(map[string]any)
			h := &mapHandler{attrs: captured}
			logger := slog.New(h)

			LogWithLogger(tt.settings, logger)

			if tt.name == "stdio transport" {
				assert.Contains(t, captured, "Config: content_dir")
				assert.Contains(t, captured, "Config: transport")
				assert.NotContains(t, captured, "Config: host")
				assert.Equal(t, SearchResultModeReferences, captured["Config: search.result_mode"])
			}
			if tt.settings.Auth.Type == AuthTypeBasic {
				assert.Contains(t, captured, "Config: auth.basic.username")
				assert.Equal(t, "user", captured["Config: auth.basic.username"])
				assert.Contains(t, captured, "Config: auth.basic.password")
				assert.Equal(t, "****", captured["Config: auth.basic.password"])
			}
			if tt.settings.Auth.Type == AuthTypeAPIKey {
				assert.Contains(t, captured, "Config: auth.api_keys")
				assert.Equal(t, int64(len(tt.settings.Auth.APIKeys)), captured["Config: auth.api_keys"])
			}
		})
	}
}

func TestLogValues(t *testing.T) {
	s := Settings{
		ContentDir: "/tmp",
		Transport:  "stdio",
		Search: SearchSettings{
			MaxResults: 10,
			ResultMode: SearchResultModeReferences,
		},
		Auth: AuthSettings{
			Type: AuthTypeBasic,
			Basic: BasicAuthSettings{
				Username: "user",
				Password: "secret-password",
			},
			APIKeys: []string{"key1", "key2"},
		},
	}

	t.Run("SettingsLogValue", func(t *testing.T) {
		val := SettingsLogValue(s)
		assert.Equal(t, slog.KindGroup, val.Kind())
		attrs := val.Group()
		attrMap := make(map[string]slog.Value)
		for _, a := range attrs {
			attrMap[a.Key] = a.Value
		}
		assert.Equal(t, "/tmp", attrMap["content_dir"].String())
		assert.Equal(t, "stdio", attrMap["transport"].String())
	})

	t.Run("SearchSettingsLogValue", func(t *testing.T) {
		val := SearchSettingsLogValue(s.Search)
		assert.Equal(t, slog.KindGroup, val.Kind())
		attrs := val.Group()
		attrMap := make(map[string]slog.Value)
		for _, a := range attrs {
			attrMap[a.Key] = a.Value
		}
		assert.Equal(t, int64(10), attrMap["max_results"].Int64())
		assert.Equal(t, "references", attrMap["result_mode"].String())
	})

	t.Run("AuthSettingsLogValue", func(t *testing.T) {
		val := AuthSettingsLogValue(s.Auth)
		assert.Equal(t, slog.KindGroup, val.Kind())
		attrs := val.Group()
		attrMap := make(map[string]slog.Value)
		for _, a := range attrs {
			attrMap[a.Key] = a.Value
		}
		assert.Equal(t, "basic", attrMap["type"].String())
		keys := attrMap["api_keys"].Any().([]string)
		assert.Equal(t, []string{"****", "****"}, keys)
	})

	t.Run("BasicAuthSettingsLogValue", func(t *testing.T) {
		val := BasicAuthSettingsLogValue(s.Auth.Basic)
		assert.Equal(t, slog.KindGroup, val.Kind())
		attrs := val.Group()
		attrMap := make(map[string]slog.Value)
		for _, a := range attrs {
			attrMap[a.Key] = a.Value
		}
		assert.Equal(t, "user", attrMap["username"].String())
		assert.Equal(t, "****", attrMap["password"].String())
	})
}

func TestLog_IncludesHeadingAndPathBoosts(t *testing.T) {
	captured := make(map[string]any)
	logger := slog.New(&mapHandler{attrs: captured})

	LogWithLogger(&Settings{
		Transport: "stdio",
		Search:    SearchSettings{HeadingBoost: 2.5, PathBoost: 1.25},
		Auth:      AuthSettings{Type: AuthTypeNone},
	}, logger)

	assert.Equal(t, 2.5, captured["Config: search.heading_boost"])
	assert.Equal(t, 1.25, captured["Config: search.path_boost"])
}

func TestSearchSettingsLogValue_IncludesHeadingAndPathBoosts(t *testing.T) {
	attrs := SearchSettingsLogValue(SearchSettings{HeadingBoost: 2.5, PathBoost: 1.25}).Group()

	found := make(map[string]float64)
	for _, attr := range attrs {
		if attr.Value.Kind() == slog.KindFloat64 {
			found[attr.Key] = attr.Value.Float64()
		}
	}
	assert.Equal(t, 2.5, found["heading_boost"])
	assert.Equal(t, 1.25, found["path_boost"])
}

// Custom handler for testing LogValue output
type mapHandler struct {
	attrs map[string]any
}

func (h *mapHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *mapHandler) Handle(_ context.Context, r slog.Record) error {
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "value" || a.Key == "count" {
			h.attrs[r.Message] = a.Value.Any()
		}
		return true
	})
	return nil
}
func (h *mapHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *mapHandler) WithGroup(name string) slog.Handler       { return h }

const uiStdioWarning = "Web UI requires the sse transport; ui.enabled is ignored"

type recordingHandler struct {
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordingHandler) warnings() []string {
	var messages []string
	for _, r := range h.records {
		if r.Level >= slog.LevelWarn {
			messages = append(messages, r.Message)
		}
	}
	return messages
}

func TestLogWithLogger_UIStdioWarning(t *testing.T) {
	tests := []struct {
		name         string
		transport    string
		enabled      bool
		wantWarnings []string
	}{
		{name: "stdio enabled", transport: "stdio", enabled: true, wantWarnings: []string{uiStdioWarning}},
		{name: "stdio disabled", transport: "stdio", enabled: false},
		{name: "sse enabled", transport: "sse", enabled: true},
		{name: "sse disabled", transport: "sse", enabled: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &recordingHandler{}

			LogWithLogger(&Settings{
				Transport: tt.transport,
				Auth:      AuthSettings{Type: AuthTypeNone},
				UI:        UISettings{Enabled: tt.enabled, MaxResults: DefaultUIMaxResults},
			}, slog.New(h))

			assert.Equal(t, tt.wantWarnings, h.warnings())
		})
	}
}

func TestLogWithLogger_LogsUISettings(t *testing.T) {
	captured := make(map[string]any)

	LogWithLogger(&Settings{
		Transport: "sse",
		Auth:      AuthSettings{Type: AuthTypeNone},
		UI:        UISettings{Enabled: true, MaxResults: 42},
	}, slog.New(&mapHandler{attrs: captured}))

	assert.Equal(t, true, captured["Config: ui.enabled"])
	assert.Equal(t, int64(42), captured["Config: ui.max_results"])
}

func TestLogWithLogger_LogsUIEnabledWhenDisabled(t *testing.T) {
	captured := make(map[string]any)

	LogWithLogger(&Settings{
		Transport: "sse",
		Auth:      AuthSettings{Type: AuthTypeNone},
		UI:        UISettings{Enabled: false},
	}, slog.New(&mapHandler{attrs: captured}))

	assert.Equal(t, false, captured["Config: ui.enabled"])
}

func TestSettingsLogValue_IncludesUIGroup(t *testing.T) {
	attrs := SettingsLogValue(Settings{UI: UISettings{Enabled: true, MaxResults: 42}}).Group()

	var ui slog.Value
	found := false
	for _, a := range attrs {
		if a.Key == "ui" {
			ui, found = a.Value.Resolve(), true
		}
	}
	require.True(t, found, "SettingsLogValue must include a \"ui\" attribute")
	require.Equal(t, slog.KindGroup, ui.Kind())
	uiAttrs := groupAttrs(ui)
	assert.Equal(t, true, uiAttrs["enabled"].Bool())
	assert.Equal(t, int64(42), uiAttrs["max_results"].Int64())
}

func dumpTestSettings(transport string, auth AuthSettings, ui UISettings) *Settings {
	return &Settings{
		ContentDir: "/content dir",
		Transport:  transport,
		Host:       "0.0.0.0",
		Port:       8080,
		Search: SearchSettings{
			MaxResults: 10, InMemory: true, KeywordsBoost: 3, HeadingBoost: 2.5,
			PathBoost: 2, TitleBoost: 1.5, ContentBoost: 1, ResultMode: SearchResultModeReferences,
		},
		Auth:         auth,
		UI:           ui,
		ServerMode:   ServerModeShared,
		SharedServer: SharedServerSettings{ConnectWait: 5 * time.Second, Linger: time.Minute},
	}
}

func dumpTestOutput(write func(*slog.Logger)) string {
	var buf bytes.Buffer
	write(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})))
	return buf.String()
}

const dumpTestIdentityLines = `level=INFO msg="Config: search.max_results" value=10
level=INFO msg="Config: search.in_memory" value=true
level=INFO msg="Config: search.keywords_boost" value=3
level=INFO msg="Config: search.heading_boost" value=2.5
level=INFO msg="Config: search.path_boost" value=2
level=INFO msg="Config: search.title_boost" value=1.5
level=INFO msg="Config: search.content_boost" value=1
level=INFO msg="Config: search.result_mode" value=references
`

func TestLogWithLogger_DumpIsByteIdentical(t *testing.T) {
	tests := []struct {
		name     string
		settings *Settings
		expected string
	}{
		{
			name: "sse with basic auth and web UI",
			settings: dumpTestSettings("sse",
				AuthSettings{Type: AuthTypeBasic, Basic: BasicAuthSettings{Username: "alice", Password: "secret"}},
				UISettings{Enabled: true, MaxResults: 20}),
			expected: `level=INFO msg="Config: content_dir" value="/content dir"
level=INFO msg="Config: transport" value=sse
level=INFO msg="Config: host" value=0.0.0.0
level=INFO msg="Config: port" value=8080
level=INFO msg="Config: server_mode" value=shared
level=INFO msg="Config: shared_server.connect_wait" value=5s
level=INFO msg="Config: shared_server.linger" value=1m0s
` + dumpTestIdentityLines + `level=INFO msg="Config: auth.type" value=basic
level=INFO msg="Config: auth.basic.username" value=alice
level=INFO msg="Config: auth.basic.password" value=****
level=INFO msg="Config: ui.enabled" value=true
level=INFO msg="Config: ui.max_results" value=20
`,
		},
		{
			name: "stdio with api keys and ignored web UI",
			settings: dumpTestSettings("stdio",
				AuthSettings{Type: AuthTypeAPIKey, APIKeys: []string{"k1", "k2"}},
				UISettings{Enabled: true, MaxResults: 20}),
			expected: `level=INFO msg="Config: content_dir" value="/content dir"
level=INFO msg="Config: transport" value=stdio
level=INFO msg="Config: server_mode" value=shared
level=INFO msg="Config: shared_server.connect_wait" value=5s
level=INFO msg="Config: shared_server.linger" value=1m0s
` + dumpTestIdentityLines + `level=INFO msg="Config: auth.type" value=apikey
level=INFO msg="Config: auth.api_keys" count=2
level=INFO msg="Config: ui.enabled" value=true
level=INFO msg="Config: ui.max_results" value=20
level=WARN msg="Web UI requires the sse transport; ui.enabled is ignored"
`,
		},
		{
			name: "stdio without auth or web UI",
			settings: dumpTestSettings("stdio",
				AuthSettings{Type: AuthTypeNone},
				UISettings{Enabled: false, MaxResults: 20}),
			expected: `level=INFO msg="Config: content_dir" value="/content dir"
level=INFO msg="Config: transport" value=stdio
level=INFO msg="Config: server_mode" value=shared
level=INFO msg="Config: shared_server.connect_wait" value=5s
level=INFO msg="Config: shared_server.linger" value=1m0s
` + dumpTestIdentityLines + `level=INFO msg="Config: auth.type" value=none
level=INFO msg="Config: ui.enabled" value=false
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dumpTestOutput(func(logger *slog.Logger) { LogWithLogger(tt.settings, logger) })

			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestLogIdentitySettings_DumpsOnlyIdentityRelevantLines(t *testing.T) {
	variants := map[string]*Settings{
		"sse with basic auth and web UI": dumpTestSettings("sse",
			AuthSettings{Type: AuthTypeBasic, Basic: BasicAuthSettings{Username: "alice", Password: "secret"}},
			UISettings{Enabled: true, MaxResults: 20}),
		"stdio with api keys and ignored web UI": dumpTestSettings("stdio",
			AuthSettings{Type: AuthTypeAPIKey, APIKeys: []string{"k1", "k2"}},
			UISettings{Enabled: true, MaxResults: 20}),
		"stdio without auth or web UI": dumpTestSettings("stdio",
			AuthSettings{Type: AuthTypeNone},
			UISettings{Enabled: false, MaxResults: 20}),
	}

	for name, settings := range variants {
		t.Run(name, func(t *testing.T) {
			got := dumpTestOutput(func(logger *slog.Logger) { LogIdentitySettings(settings, logger) })

			assert.Equal(t, `level=INFO msg="Config: content_dir" value="/content dir"
`+dumpTestIdentityLines, got)
		})
	}
}

func TestDumpLines_EveryLineIsClassified(t *testing.T) {
	require.NotEmpty(t, dumpLines)
	for i, line := range dumpLines {
		assert.Contains(t, []dumpScope{identityScope, sessionScope}, line.scope, "dump line %d has no scope", i)
	}
}

const dumpTestZeroSessionLines = `level=INFO msg="Config: server_mode" value=""
level=INFO msg="Config: shared_server.connect_wait" value=0s
level=INFO msg="Config: shared_server.linger" value=0s
level=INFO msg="Config: search.max_results" value=0
level=INFO msg="Config: search.in_memory" value=false
level=INFO msg="Config: search.keywords_boost" value=0
level=INFO msg="Config: search.heading_boost" value=0
level=INFO msg="Config: search.path_boost" value=0
level=INFO msg="Config: search.title_boost" value=0
level=INFO msg="Config: search.content_boost" value=0
level=INFO msg="Config: search.result_mode" value=""
`

func TestLogWithLogger_EdgeValueDumpIsByteIdentical(t *testing.T) {
	tests := []struct {
		name     string
		settings *Settings
		expected string
	}{
		{
			name:     "sse with web UI disabled, zero values and no api keys",
			settings: &Settings{Transport: "sse", Auth: AuthSettings{Type: AuthTypeAPIKey}},
			expected: `level=INFO msg="Config: content_dir" value=""
level=INFO msg="Config: transport" value=sse
level=INFO msg="Config: host" value=""
level=INFO msg="Config: port" value=0
` + dumpTestZeroSessionLines + `level=INFO msg="Config: auth.type" value=apikey
level=INFO msg="Config: auth.api_keys" count=0
level=INFO msg="Config: ui.enabled" value=false
`,
		},
		{
			name:     "stdio with ignored web UI, zero values and empty basic credentials",
			settings: &Settings{Transport: "stdio", Auth: AuthSettings{Type: AuthTypeBasic}, UI: UISettings{Enabled: true}},
			expected: `level=INFO msg="Config: content_dir" value=""
level=INFO msg="Config: transport" value=stdio
` + dumpTestZeroSessionLines + `level=INFO msg="Config: auth.type" value=basic
level=INFO msg="Config: auth.basic.username" value=""
level=INFO msg="Config: auth.basic.password" value=****
level=INFO msg="Config: ui.enabled" value=true
level=INFO msg="Config: ui.max_results" value=0
level=WARN msg="Web UI requires the sse transport; ui.enabled is ignored"
`,
		},
		{
			name: "sse with web UI disabled",
			settings: dumpTestSettings("sse",
				AuthSettings{Type: AuthTypeNone},
				UISettings{Enabled: false, MaxResults: 20}),
			expected: `level=INFO msg="Config: content_dir" value="/content dir"
level=INFO msg="Config: transport" value=sse
level=INFO msg="Config: host" value=0.0.0.0
level=INFO msg="Config: port" value=8080
level=INFO msg="Config: server_mode" value=shared
level=INFO msg="Config: shared_server.connect_wait" value=5s
level=INFO msg="Config: shared_server.linger" value=1m0s
` + dumpTestIdentityLines + `level=INFO msg="Config: auth.type" value=none
level=INFO msg="Config: ui.enabled" value=false
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dumpTestOutput(func(logger *slog.Logger) { LogWithLogger(tt.settings, logger) })

			assert.Equal(t, tt.expected, got)
		})
	}
}

// knownUndumpedSettings lists the settings leaves the dump does not report
// yet. Adding their lines changes the dump output and is tracked by #201;
// no other leaf may be missing from the dump.
var knownUndumpedSettings = []string{"uri_scheme", "cross_ref"}

func TestLogWithLogger_DumpsEverySettingsLeaf(t *testing.T) {
	everyConditionalLine := []*Settings{
		dumpTestSettings("sse",
			AuthSettings{Type: AuthTypeBasic, Basic: BasicAuthSettings{Username: "alice", Password: "secret"}},
			UISettings{Enabled: true, MaxResults: 20}),
		dumpTestSettings("stdio",
			AuthSettings{Type: AuthTypeAPIKey, APIKeys: []string{"k1"}},
			UISettings{Enabled: true, MaxResults: 20}),
	}
	dumped := map[string]bool{}
	for _, s := range everyConditionalLine {
		handler := &recordingHandler{}
		LogWithLogger(s, slog.New(handler))
		for _, r := range handler.records {
			if key, ok := strings.CutPrefix(r.Message, "Config: "); ok {
				dumped[key] = true
			}
		}
	}

	leaves := dumpTestSettingsLeaves(reflect.TypeFor[Settings](), "")

	for _, leaf := range leaves {
		if slices.Contains(knownUndumpedSettings, leaf) {
			assert.False(t, dumped[leaf], "%s is dumped; remove it from knownUndumpedSettings", leaf)
			continue
		}
		assert.True(t, dumped[leaf], "settings leaf %s has no dump line", leaf)
	}
	for _, leaf := range knownUndumpedSettings {
		assert.Contains(t, leaves, leaf, "knownUndumpedSettings names %s, which is no settings leaf", leaf)
	}
}

func dumpTestSettingsLeaves(t reflect.Type, prefix string) []string {
	var leaves []string
	for i := range t.NumField() {
		field := t.Field(i)
		key := prefix + field.Tag.Get("mapstructure")
		if field.Type.Kind() == reflect.Struct {
			leaves = append(leaves, dumpTestSettingsLeaves(field.Type, key+".")...)
			continue
		}
		leaves = append(leaves, key)
	}
	return leaves
}
