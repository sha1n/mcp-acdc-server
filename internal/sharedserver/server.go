package sharedserver

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrLockNotAcquired reports that another process holds the shared server
// lock, or that the lock could not be taken. The caller must exit without
// writing anything.
var ErrLockNotAcquired = errors.New("shared server lock not acquired")

// ErrIdentityMismatch reports that the content on disk no longer matches the
// shared server identity, or that its identity cannot be recomputed. The
// shared server has logged it and exits before binding.
var ErrIdentityMismatch = errors.New("shared server identity does not match its content")

// BuildFunc builds the catalog, index and MCP server, logging through the
// shared server's logger. The returned cleanup, if any, runs after the last
// session ends.
type BuildFunc func(context.Context, *slog.Logger) (*mcp.Server, func(), error)

// Config describes one shared server.
type Config struct {
	Identity    string
	EndpointDir string
	// LogDir holds <identity>.log; an empty or unusable directory discards
	// the log instead of preventing the shared server from serving.
	LogDir string
	// LogStartup writes the shared server's startup entries once its log is
	// open, so a candidate that loses the lock writes nothing.
	LogStartup func(*slog.Logger)
	// CurrentIdentity recomputes the identity from the content on disk with
	// the settings and binary of this process. It is required: a shared
	// server whose content changed after its spawning session computed the
	// identity must not serve under that identity.
	CurrentIdentity func() (string, error)
	Build           BuildFunc
	// Revalidate, if set, runs for every accepted connection before its ready
	// line, so a session's first listing reflects current content. It is
	// called only after Build has returned.
	Revalidate func(context.Context)
	// UIHandler, if set, builds the Web UI handler on the first browse
	// request. It is called only after Build has returned. Without it every
	// browse request is answered with an error.
	UIHandler UIHandlerFunc
	// UIListen, if set, binds the Web UI instead of an ephemeral port on
	// 127.0.0.1.
	UIListen func() (net.Listener, error)
	// Linger below config.MinSharedServerLinger is rejected before anything
	// is created, as settings validation rejects it.
	Linger time.Duration
}
