package config

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
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
					NameBoost:     1.0,
					ContentBoost:  1.0,
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
