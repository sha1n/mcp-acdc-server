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
// session sharing this server agrees on.
func logSharedServerStartup(logger *slog.Logger, s *config.Settings, version string) {
	logger.InfoContext(context.Background(), "Starting MCP Acdc shared server", "version", version, "pid", os.Getpid())
	config.LogIdentitySettings(s, logger)
}
