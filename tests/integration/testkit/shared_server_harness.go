//go:build unix

package testkit

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

// SharedServerDeadline bounds every poll and wait of the in-process tier.
const SharedServerDeadline = 30 * time.Second

const pollInterval = 10 * time.Millisecond

// SharedServerHarness runs session proxies and the shared servers they spawn
// as goroutines of the test process, over a real socket and a real lock.
type SharedServerHarness struct {
	t testing.TB
	// EndpointDir is not created by the harness, so its absence stays
	// observable until a launch opens it.
	EndpointDir string
	LogDir      string
	Binary      identity.Binary

	ctx     context.Context
	spawns  atomic.Int32
	builds  atomic.Int32
	servers sync.WaitGroup
	exitsMu sync.Mutex
	exits   []SharedServerExit

	launchesMu sync.Mutex
	launches   []*LaunchUnderTest
}

// SharedServerExit is how one spawned candidate ended.
type SharedServerExit struct {
	Built     bool
	Err       error
	SpawnedAt time.Time
	ExitedAt  time.Time
}

// NewSharedServerHarness stops every shared server it spawned when the test
// ends and fails the test if one does not stop within SharedServerDeadline.
func NewSharedServerHarness(t testing.TB) *SharedServerHarness {
	t.Helper()
	// t.TempDir() is not used: its macOS paths exceed the socket path budget.
	base, err := os.MkdirTemp("/tmp", "acdc-ip-")
	require.NoError(t, err)
	t.Cleanup(func() { removeAll(t, base) })

	ctx, cancel := context.WithCancel(context.Background())
	h := &SharedServerHarness{
		t:           t,
		EndpointDir: filepath.Join(base, "endpoint"),
		LogDir:      t.TempDir(),
		// A stamped build keeps the identity independent of the test binary.
		Binary: identity.Binary{Version: "1.0.0-test", Build: "test"},
		ctx:    ctx,
	}
	t.Cleanup(func() {
		cancel()
		h.awaitServers()
	})
	return h
}

// SharedLaunchArgs returns the arguments of a stdio launch in shared mode with
// the shortest linger the public settings allow; later arguments override.
func SharedLaunchArgs(contentDir string, extra ...string) []string {
	args := []string{
		"--content-dir", contentDir,
		"--transport", "stdio",
		"--server-mode", "shared",
		"--shared-server-linger", "1s",
		"--shared-server-connect-wait", "5s",
	}
	return append(args, extra...)
}

// Spawns counts candidates started through the spawn seam.
func (h *SharedServerHarness) Spawns() int { return int(h.spawns.Load()) }

// Builds counts candidates that won the lock and built their MCP server.
func (h *SharedServerHarness) Builds() int { return int(h.builds.Load()) }

// Eventually polls cond until it holds or SharedServerDeadline passes.
func (h *SharedServerHarness) Eventually(cond func() bool, msg string) {
	h.t.Helper()
	require.Eventually(h.t, cond, SharedServerDeadline, pollInterval, msg)
}

// Identity computes the server identity a launch with args would compute.
func (h *SharedServerHarness) Identity(args []string) string {
	h.t.Helper()
	settings, err := config.LoadSettingsWithFlags(LaunchFlags(h.t, args))
	require.NoError(h.t, err)
	id, err := identity.Compute(settings, h.Binary)
	require.NoError(h.t, err)
	return id
}

// Endpoint opens the harness endpoint directory, creating it if needed.
func (h *SharedServerHarness) Endpoint() endpoint.Endpoint {
	h.t.Helper()
	ep, err := endpoint.OpenEndpoint(h.EndpointDir)
	require.NoError(h.t, err)
	return ep
}

// EndpointDirExists reports whether any launch has created the endpoint directory.
func (h *SharedServerHarness) EndpointDirExists() bool {
	h.t.Helper()
	_, err := os.Lstat(h.EndpointDir)
	if os.IsNotExist(err) {
		return false
	}
	require.NoError(h.t, err)
	return true
}

// HoldLock takes the lock of id as a shutting-down shared server would hold
// it. The returned release is idempotent and also runs when the test ends.
func (h *SharedServerHarness) HoldLock(id string) (release func()) {
	h.t.Helper()
	lockPath, err := h.Endpoint().LockPath(id)
	require.NoError(h.t, err)
	lock := flock.New(lockPath)
	locked, err := lock.TryLock()
	require.NoError(h.t, err)
	require.True(h.t, locked, "lock of %s is already held", id)

	var once sync.Once
	release = func() {
		once.Do(func() {
			if err := lock.Unlock(); err != nil {
				h.t.Errorf("failed to release lock: %v", err)
			}
		})
	}
	h.t.Cleanup(release)
	return release
}

// SpawnSharedServer returns the spawn seam of a launch with args: like the
// production re-exec it starts the same launch plus the hidden identity flag,
// here as a goroutine running the runner.
func (h *SharedServerHarness) SpawnSharedServer(args []string) func(string) error {
	args = slices.Clone(args)
	return func(id string) error {
		h.spawns.Add(1)
		flags, err := newLaunchFlags(append(slices.Clone(args), "--shared-server-identity", id))
		if err != nil {
			return err
		}
		var built atomic.Bool
		params := h.sharedServerParams(&built)
		spawnedAt := time.Now()
		h.servers.Add(1)
		go func() {
			defer h.servers.Done()
			err := app.RunWithDeps(h.ctx, params, flags, h.Binary)
			h.recordExit(SharedServerExit{Built: built.Load(), Err: err, SpawnedAt: spawnedAt, ExitedAt: time.Now()})
			if err != nil {
				h.t.Logf("shared server %s stopped with: %v", id, err)
			}
		}()
		return nil
	}
}

// SessionProxyParams returns the run parameters of a launch with args whose
// spawn seam starts in-process shared servers.
func (h *SharedServerHarness) SessionProxyParams(args []string) app.RunParams {
	params := app.DefaultRunParams()
	params.EndpointDir = h.endpointDir
	params.LogDir = h.logDir
	params.SpawnSharedServer = h.SpawnSharedServer(args)
	return params
}

// SharedServerExits returns how every spawned candidate that has ended so
// far ended.
func (h *SharedServerHarness) SharedServerExits() []SharedServerExit {
	h.exitsMu.Lock()
	defer h.exitsMu.Unlock()
	return slices.Clone(h.exits)
}

func (h *SharedServerHarness) recordExit(exit SharedServerExit) {
	h.exitsMu.Lock()
	defer h.exitsMu.Unlock()
	h.exits = append(h.exits, exit)
}

func (h *SharedServerHarness) sharedServerParams(built *atomic.Bool) app.RunParams {
	params := app.DefaultRunParams()
	createServer := params.CreateServer
	params.CreateServer = func(ctx context.Context, logger *slog.Logger, settings *config.Settings, version string) (app.Components, error) {
		built.Store(true)
		h.builds.Add(1)
		return createServer(ctx, logger, settings, version)
	}
	params.EndpointDir = h.endpointDir
	params.LogDir = h.logDir
	// A re-exec'd shared server has the null device as its standard streams.
	params.Stderr = io.Discard
	params.Stdin = nil
	params.Stdout = nil
	return params
}

func (h *SharedServerHarness) endpointDir() (string, error) { return h.EndpointDir, nil }

func (h *SharedServerHarness) logDir() (string, error) { return h.LogDir, nil }

func (h *SharedServerHarness) awaitServers() {
	stopped := make(chan struct{})
	go func() {
		h.servers.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(SharedServerDeadline):
		h.t.Errorf("spawned shared servers did not stop within %s", SharedServerDeadline)
	}
}

// LaunchFlags builds the flag set the binary would parse from args.
func LaunchFlags(t testing.TB, args []string) *pflag.FlagSet {
	t.Helper()
	flags, err := newLaunchFlags(args)
	require.NoError(t, err)
	return flags
}

func newLaunchFlags(args []string) (*pflag.FlagSet, error) {
	flags := pflag.NewFlagSet("acdc-mcp", pflag.ContinueOnError)
	app.RegisterFlags(flags)
	return flags, flags.Parse(args)
}

func removeAll(t testing.TB, dir string) {
	if err := os.RemoveAll(dir); err != nil {
		t.Errorf("failed to remove %s: %v", dir, err)
	}
}
