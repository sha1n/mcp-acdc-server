package app

import (
	"context"
	"io"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
)

// runPerSession serves one session in this process. A session proxy whose
// discovery failed uses it too, so a fallback starts exactly as a stdio
// launch does today.
func runPerSession(ctx context.Context, logger *slog.Logger, params RunParams, settings *config.Settings, version string, transport mcp.Transport) error {
	components, err := params.CreateServer(ctx, logger, settings, version)
	if err != nil {
		return err
	}
	if components.Cleanup != nil {
		defer components.Cleanup()
	}
	return components.MCP.Run(ctx, transport)
}

// hostOutput keeps the host's stdout open when the per-session server's
// transport closes: the stream belongs to the launch, as with mcp.StdioTransport.
type hostOutput struct{ io.Writer }

func (hostOutput) Close() error { return nil }
