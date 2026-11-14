package app

import (
	"context"
	"log/slog"
	"os"

	"github.com/sha1n/mcp-acdc-server/internal/config"
)

// sharedServerLogDir reports an unresolvable log directory as none: the
// shared server then serves with its log discarded rather than failing.
func sharedServerLogDir(params RunParams) string {
	if params.LogDir == nil {
		return ""
	}
	dir, err := params.LogDir()
	if err != nil {
		return ""
	}
	return dir
}

// logSharedServerStartup writes the startup entry and the settings that every
// session sharing this server agrees on. Only the identity-excluded settings
// (transport, host, port, auth, UI, server mode, shared server) may differ
// between those sessions, so only they are left out; any other setting is
// identity-relevant and belongs here, which the comparison with config.Log in
// the tests enforces.
func logSharedServerStartup(logger *slog.Logger, s *config.Settings, version string) {
	ctx := context.Background()
	logger.InfoContext(ctx, "Starting MCP Acdc shared server", "version", version, "pid", os.Getpid())
	logger.InfoContext(ctx, "Config: content_dir", "value", s.ContentDir)
	logger.InfoContext(ctx, "Config: search.max_results", "value", s.Search.MaxResults)
	logger.InfoContext(ctx, "Config: search.in_memory", "value", s.Search.InMemory)
	logger.InfoContext(ctx, "Config: search.keywords_boost", "value", s.Search.KeywordsBoost)
	logger.InfoContext(ctx, "Config: search.heading_boost", "value", s.Search.HeadingBoost)
	logger.InfoContext(ctx, "Config: search.path_boost", "value", s.Search.PathBoost)
	logger.InfoContext(ctx, "Config: search.title_boost", "value", s.Search.TitleBoost)
	logger.InfoContext(ctx, "Config: search.content_boost", "value", s.Search.ContentBoost)
	logger.InfoContext(ctx, "Config: search.result_mode", "value", s.Search.ResultMode)
}
