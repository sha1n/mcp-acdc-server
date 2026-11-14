package app

import (
	"log/slog"

	"github.com/sha1n/mcp-acdc-server/internal/config"
)

// effectiveServerMode takes the platform as a value so every platform's
// resolution is testable on any OS. Windows has no shared server; it is not a
// fallback, so only an overridden shared mode is logged, at Debug, and never
// warns; a configured session mode needs no override and logs nothing.
func effectiveServerMode(logger *slog.Logger, goos string, configured config.ServerMode) config.ServerMode {
	if goos != "windows" {
		return configured
	}
	if configured == config.ServerModeShared {
		logger.Debug("Shared server is unavailable on Windows; the launch behaves as session", "server_mode", configured)
	}
	return config.ServerModeSession
}
