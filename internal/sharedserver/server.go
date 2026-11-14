package sharedserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrLockNotAcquired reports that another process holds the shared server
// lock, or that the lock could not be taken. The caller must exit without
// writing anything.
var ErrLockNotAcquired = errors.New("shared server lock not acquired")

// BuildFunc builds the catalog, index and MCP server. The returned cleanup,
// if any, runs after the last session ends.
type BuildFunc func(context.Context) (*mcp.Server, func(), error)

// Config describes one shared server.
type Config struct {
	Identity    string
	EndpointDir string
	Build       BuildFunc
	// Revalidate, if set, runs for every accepted connection before its ready
	// line, so a session's first listing reflects current content. It is
	// called only after Build has returned.
	Revalidate func(context.Context)
}
