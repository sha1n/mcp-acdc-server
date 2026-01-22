package config

import (
	"context"
	"log/slog"
)

// Log logs the resolved settings in a granular way, skipping irrelevant ones
func Log(s *Settings) {
	LogWithLogger(s, slog.Default())
}

// LogWithLogger logs the resolved settings using the provided logger
func LogWithLogger(s *Settings, logger *slog.Logger) {
	ctx := context.Background()
	logger.InfoContext(ctx, "Config: content_dir", "value", s.ContentDir)
	logger.InfoContext(ctx, "Config: transport", "value", s.Transport)
	if s.Transport == "sse" {
		logger.InfoContext(ctx, "Config: host", "value", s.Host)
		logger.InfoContext(ctx, "Config: port", "value", s.Port)
	}

	logger.InfoContext(ctx, "Config: search.max_results", "value", s.Search.MaxResults)
	logger.InfoContext(ctx, "Config: search.in_memory", "value", s.Search.InMemory)
	logger.InfoContext(ctx, "Config: search.keywords_boost", "value", s.Search.KeywordsBoost)
	logger.InfoContext(ctx, "Config: search.name_boost", "value", s.Search.NameBoost)
	logger.InfoContext(ctx, "Config: search.content_boost", "value", s.Search.ContentBoost)

	logger.InfoContext(ctx, "Config: auth.type", "value", s.Auth.Type)
	switch s.Auth.Type {
	case AuthTypeBasic:
		logger.InfoContext(ctx, "Config: auth.basic.username", "value", s.Auth.Basic.Username)
		logger.InfoContext(ctx, "Config: auth.basic.password", "value", "****")
	case AuthTypeAPIKey:
		logger.InfoContext(ctx, "Config: auth.api_keys", "count", len(s.Auth.APIKeys))
	}
}

// SearchSettingsLogValue returns a slog.Value for SearchSettings with masked data if needed
func SearchSettingsLogValue(s SearchSettings) slog.Value {
	return slog.GroupValue(
		slog.Int("max_results", s.MaxResults),
		slog.Bool("in_memory", s.InMemory),
		slog.Float64("keywords_boost", s.KeywordsBoost),
		slog.Float64("name_boost", s.NameBoost),
		slog.Float64("content_boost", s.ContentBoost),
	)
}

// AuthSettingsLogValue returns a slog.Value for AuthSettings with masked data
func AuthSettingsLogValue(s AuthSettings) slog.Value {
	keys := make([]string, len(s.APIKeys))
	for i := range s.APIKeys {
		keys[i] = "****"
	}
	return slog.GroupValue(
		slog.String("type", s.Type),
		slog.Any("basic", BasicAuthSettingsLogValue(s.Basic)),
		slog.Any("api_keys", keys),
	)
}

// BasicAuthSettingsLogValue returns a slog.Value for BasicAuthSettings with masked data
func BasicAuthSettingsLogValue(s BasicAuthSettings) slog.Value {
	return slog.GroupValue(
		slog.String("username", s.Username),
		slog.String("password", "****"),
	)
}

// SettingsLogValue returns a slog.Value for Settings with masked data
func SettingsLogValue(s Settings) slog.Value {
	return slog.GroupValue(
		slog.String("content_dir", s.ContentDir),
		slog.String("transport", s.Transport),
		slog.String("host", s.Host),
		slog.Int("port", s.Port),
		slog.Any("search", SearchSettingsLogValue(s.Search)),
		slog.Any("auth", AuthSettingsLogValue(s.Auth)),
	)
}
