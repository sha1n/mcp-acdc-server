package config

import (
	"context"
	"log/slog"
)

// LogWithLogger logs the resolved settings using the provided logger
func LogWithLogger(s *Settings, logger *slog.Logger) {
	writeDumpLines(s, logger, func(dumpScope) bool { return true })
}

// LogIdentitySettings logs the lines of the settings dump that every session
// of one server identity agrees on: the dump of LogWithLogger without the
// settings excluded from the identity.
func LogIdentitySettings(s *Settings, logger *slog.Logger) {
	writeDumpLines(s, logger, func(scope dumpScope) bool { return scope == identityScope })
}

type dumpScope int

const (
	// The zero value is deliberately no scope, so a dump line added without a
	// classification is caught instead of silently defaulting to one.
	_ dumpScope = iota
	identityScope
	sessionScope
)

type dumpLine struct {
	scope dumpScope
	write func(ctx context.Context, logger *slog.Logger, s *Settings)
}

// dumpLines is the settings dump in output order. A line is identityScope
// exactly when the setting it reports takes part in the server identity.
var dumpLines = []dumpLine{
	valueLine(identityScope, "content_dir", func(s *Settings) any { return s.ContentDir }),
	valueLine(identityScope, "uri_scheme", func(s *Settings) any { return s.Scheme }),
	valueLine(identityScope, "cross_ref", func(s *Settings) any { return s.CrossRef }),
	valueLine(sessionScope, "transport", func(s *Settings) any { return s.Transport }),
	onlyIf(usesSSE, valueLine(sessionScope, "host", func(s *Settings) any { return s.Host })),
	onlyIf(usesSSE, valueLine(sessionScope, "port", func(s *Settings) any { return s.Port })),

	valueLine(sessionScope, "server_mode", func(s *Settings) any { return s.ServerMode }),
	valueLine(sessionScope, "shared_server.connect_wait", func(s *Settings) any { return s.SharedServer.ConnectWait }),
	valueLine(sessionScope, "shared_server.linger", func(s *Settings) any { return s.SharedServer.Linger }),

	valueLine(identityScope, "search.max_results", func(s *Settings) any { return s.Search.MaxResults }),
	valueLine(identityScope, "search.in_memory", func(s *Settings) any { return s.Search.InMemory }),
	valueLine(identityScope, "search.keywords_boost", func(s *Settings) any { return s.Search.KeywordsBoost }),
	valueLine(identityScope, "search.heading_boost", func(s *Settings) any { return s.Search.HeadingBoost }),
	valueLine(identityScope, "search.path_boost", func(s *Settings) any { return s.Search.PathBoost }),
	valueLine(identityScope, "search.title_boost", func(s *Settings) any { return s.Search.TitleBoost }),
	valueLine(identityScope, "search.content_boost", func(s *Settings) any { return s.Search.ContentBoost }),
	valueLine(identityScope, "search.result_mode", func(s *Settings) any { return s.Search.ResultMode }),

	valueLine(sessionScope, "auth.type", func(s *Settings) any { return s.Auth.Type }),
	onlyIf(usesAuth(AuthTypeBasic), valueLine(sessionScope, "auth.basic.username", func(s *Settings) any { return s.Auth.Basic.Username })),
	onlyIf(usesAuth(AuthTypeBasic), valueLine(sessionScope, "auth.basic.password", func(*Settings) any { return "****" })),
	onlyIf(usesAuth(AuthTypeAPIKey), dumpLine{sessionScope, func(ctx context.Context, logger *slog.Logger, s *Settings) {
		logger.InfoContext(ctx, "Config: auth.api_keys", "count", len(s.Auth.APIKeys))
	}}),

	valueLine(sessionScope, "ui.enabled", func(s *Settings) any { return s.UI.Enabled }),
	onlyIf(enablesUI, valueLine(sessionScope, "ui.max_results", func(s *Settings) any { return s.UI.MaxResults })),
	onlyIf(ignoresUI, dumpLine{sessionScope, func(ctx context.Context, logger *slog.Logger, _ *Settings) {
		logger.WarnContext(ctx, "Web UI requires the sse transport; ui.enabled is ignored")
	}}),
}

func writeDumpLines(s *Settings, logger *slog.Logger, include func(dumpScope) bool) {
	ctx := context.Background()
	for _, line := range dumpLines {
		if include(line.scope) {
			line.write(ctx, logger, s)
		}
	}
}

func valueLine(scope dumpScope, key string, value func(*Settings) any) dumpLine {
	return dumpLine{scope, func(ctx context.Context, logger *slog.Logger, s *Settings) {
		logger.InfoContext(ctx, "Config: "+key, "value", value(s))
	}}
}

func onlyIf(condition func(*Settings) bool, line dumpLine) dumpLine {
	return dumpLine{line.scope, func(ctx context.Context, logger *slog.Logger, s *Settings) {
		if condition(s) {
			line.write(ctx, logger, s)
		}
	}}
}

func usesSSE(s *Settings) bool { return s.Transport == "sse" }

func usesAuth(authType string) func(*Settings) bool {
	return func(s *Settings) bool { return s.Auth.Type == authType }
}

func enablesUI(s *Settings) bool { return s.UI.Enabled }

func ignoresUI(s *Settings) bool { return s.UI.Enabled && s.Transport == "stdio" }
