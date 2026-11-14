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

// logSharedServerStartup writes the startup entry and an allowlist of
// settings. An allowlist rather than a filtered config.Log keeps a setting
// added later out of the shared log until it is judged identity-relevant:
// the excluded transport, host, port, auth, UI, server mode and shared server
// settings may differ between the sessions sharing one server.
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
