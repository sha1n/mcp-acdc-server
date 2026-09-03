package config

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// ParseLogLevel maps a configured level name to its slog.Level. It is the one
// place a level name is interpreted, so validation and the handler that the
// runner installs cannot disagree about which names are accepted.
//
// An empty name resolves to DefaultLogLevel rather than failing. Unlike
// Transport or ResultMode, an absent level has an obvious meaning, and a
// Settings value built in code should not have to name one to stay valid.
func ParseLogLevel(level string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "":
		return ParseLogLevel(DefaultLogLevel)
	case LogLevelDebug:
		return slog.LevelDebug, nil
	case LogLevelInfo:
		return slog.LevelInfo, nil
	case LogLevelWarn:
		return slog.LevelWarn, nil
	case LogLevelError:
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("log level must be one of debug, info, warn, error, got: %q", level)
	}
}

// Log logs the resolved settings in a granular way, skipping irrelevant ones
func Log(s *Settings) {
	LogWithLogger(s, slog.Default())
}

// LogWithLogger logs the resolved settings using the provided logger
func LogWithLogger(s *Settings, logger *slog.Logger) {
	ctx := context.Background()
	logger.InfoContext(ctx, "Config: content_dir", "value", s.ContentDir)
	logger.InfoContext(ctx, "Config: transport", "value", s.Transport)
	logger.InfoContext(ctx, "Config: log_level", "value", s.LogLevel)
	if s.Transport == "sse" {
		logger.InfoContext(ctx, "Config: host", "value", s.Host)
		logger.InfoContext(ctx, "Config: port", "value", s.Port)
	}

	logger.InfoContext(ctx, "Config: search.max_results", "value", s.Search.MaxResults)
	logger.InfoContext(ctx, "Config: search.in_memory", "value", s.Search.InMemory)
	logger.InfoContext(ctx, "Config: search.keywords_boost", "value", s.Search.KeywordsBoost)
	logger.InfoContext(ctx, "Config: search.heading_boost", "value", s.Search.HeadingBoost)
	logger.InfoContext(ctx, "Config: search.path_boost", "value", s.Search.PathBoost)
	logger.InfoContext(ctx, "Config: search.title_boost", "value", s.Search.TitleBoost)
	logger.InfoContext(ctx, "Config: search.content_boost", "value", s.Search.ContentBoost)
	logger.InfoContext(ctx, "Config: search.result_mode", "value", s.Search.ResultMode)
	logger.InfoContext(ctx, "Config: search.semantic_model", "value", s.Search.SemanticModel)
	if s.Search.SemanticModel != "" {
		logger.InfoContext(ctx, "Config: search.semantic_floor", "value", s.Search.SemanticFloor)
	}

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
		slog.Float64("heading_boost", s.HeadingBoost),
		slog.Float64("path_boost", s.PathBoost),
		slog.Float64("title_boost", s.TitleBoost),
		slog.Float64("content_boost", s.ContentBoost),
		slog.String("result_mode", string(s.ResultMode)),
		slog.String("semantic_model", s.SemanticModel),
		slog.Float64("semantic_floor", s.SemanticFloor),
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
