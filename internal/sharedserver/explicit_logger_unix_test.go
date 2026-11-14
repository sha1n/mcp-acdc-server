//go:build unix

package sharedserver

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type explicitLoggerBuildObservation struct {
	logger        *slog.Logger
	defaultLogger *slog.Logger
}

// observingBuild reports the logger Build received and the process default
// seen during the build, after writing marker through the received logger.
func observingBuild(marker string, beforeLogging func()) (BuildFunc, <-chan explicitLoggerBuildObservation) {
	observed := make(chan explicitLoggerBuildObservation, 1)
	build := func(ctx context.Context, logger *slog.Logger) (*mcp.Server, func(), error) {
		if beforeLogging != nil {
			beforeLogging()
		}
		if logger != nil {
			logger.InfoContext(ctx, marker)
		}
		observed <- explicitLoggerBuildObservation{logger: logger, defaultLogger: slog.Default()}
		return mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil), nil, nil
	}
	return build, observed
}

type explicitLoggerRun struct {
	cancel context.CancelFunc
	done   chan error
}

func startExplicitLoggerRun(t *testing.T, cfg Config) *explicitLoggerRun {
	t.Helper()
	cfg.CurrentIdentity = func() (string, error) { return cfg.Identity, nil }
	ctx, cancel := context.WithCancel(context.Background())
	run := &explicitLoggerRun{cancel: cancel, done: make(chan error, 1)}
	go func() { run.done <- Run(ctx, cfg) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-run.done:
			if err != nil {
				t.Logf("Run stopped with: %v", err)
			}
			run.done <- err
		case <-time.After(serveTestDeadline):
			t.Error("Run did not stop")
		}
	})
	return run
}

func (r *explicitLoggerRun) stop(t *testing.T) {
	t.Helper()
	r.cancel()
	select {
	case err := <-r.done:
		r.done <- err
		require.NoError(t, err)
	case <-time.After(serveTestDeadline):
		t.Fatal("Run did not stop")
	}
}

func awaitExplicitLoggerBuild(t *testing.T, observed <-chan explicitLoggerBuildObservation) explicitLoggerBuildObservation {
	t.Helper()
	select {
	case observation := <-observed:
		return observation
	case <-time.After(serveTestDeadline):
		t.Fatal("Build was never called")
		return explicitLoggerBuildObservation{}
	}
}

func readExplicitLoggerLog(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(content)
}

func TestRun_LeavesTheProcessDefaultLoggerAndPassesTheFileLoggerToBuild(t *testing.T) {
	previous := slog.Default()
	logDir := t.TempDir()
	build, observed := observingBuild("explicit build marker", nil)
	run := startExplicitLoggerRun(t, Config{
		Identity:    testIdentity,
		EndpointDir: shortTempDir(t),
		LogDir:      logDir,
		Build:       build,
		Linger:      time.Hour,
	})

	observation := awaitExplicitLoggerBuild(t, observed)
	run.stop(t)

	require.NotNil(t, observation.logger)
	assert.NotSame(t, previous, observation.logger)
	assert.Same(t, previous, observation.defaultLogger, "Run must not install a process default during the build")
	assert.Same(t, previous, slog.Default(), "Run must not leave a process default behind")
	assert.Regexp(t, `(?m)^time=\S+ level=INFO msg="explicit build marker"$`,
		readExplicitLoggerLog(t, filepath.Join(logDir, testIdentity+".log")))
}

func TestRun_ConcurrentSharedServersEachLogOnlyToTheirOwnFile(t *testing.T) {
	logDir := t.TempDir()
	endpointDir := shortTempDir(t)
	var bothBuilding sync.WaitGroup
	bothBuilding.Add(2)
	// Both builds log only once both are in progress, so each entry is written
	// while the other shared server's log is open too.
	rendezvous := func() {
		bothBuilding.Done()
		bothBuilding.Wait()
	}
	identities := []string{testIdentity, otherLogTestIdentity}
	markers := map[string]string{
		testIdentity:         "marker of the first shared server",
		otherLogTestIdentity: "marker of the second shared server",
	}
	runs := make([]*explicitLoggerRun, 0, len(identities))
	observations := make([]<-chan explicitLoggerBuildObservation, 0, len(identities))
	for _, identity := range identities {
		build, observed := observingBuild(markers[identity], rendezvous)
		runs = append(runs, startExplicitLoggerRun(t, Config{
			Identity:    identity,
			EndpointDir: endpointDir,
			LogDir:      logDir,
			Build:       build,
			Linger:      time.Hour,
		}))
		observations = append(observations, observed)
	}

	for i := range identities {
		awaitExplicitLoggerBuild(t, observations[i])
	}
	for _, run := range runs {
		run.stop(t)
	}

	first := readExplicitLoggerLog(t, filepath.Join(logDir, testIdentity+".log"))
	second := readExplicitLoggerLog(t, filepath.Join(logDir, otherLogTestIdentity+".log"))
	assert.Regexp(t, `(?m)^time=\S+ level=INFO msg="marker of the first shared server"$`, first)
	assert.NotContains(t, first, markers[otherLogTestIdentity])
	assert.Regexp(t, `(?m)^time=\S+ level=INFO msg="marker of the second shared server"$`, second)
	assert.NotContains(t, second, markers[testIdentity])
}

func TestRun_UnopenableLogPassesADiscardingLoggerToBuild(t *testing.T) {
	previous := slog.Default()
	parent := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(parent, nil, 0o600))
	build, observed := observingBuild("discarded build marker", nil)
	run := startExplicitLoggerRun(t, Config{
		Identity:    testIdentity,
		EndpointDir: shortTempDir(t),
		LogDir:      filepath.Join(parent, "acdc-mcp"),
		Build:       build,
		Linger:      time.Hour,
	})

	observation := awaitExplicitLoggerBuild(t, observed)
	run.stop(t)

	require.NotNil(t, observation.logger)
	assert.False(t, observation.logger.Enabled(context.Background(), slog.LevelError), "a discarded log must not format entries")
	assert.Same(t, previous, observation.defaultLogger)
	assert.Same(t, previous, slog.Default())
}

func TestStartLog_ReturnsTheFileLoggerWithoutInstallingAProcessDefault(t *testing.T) {
	previous := slog.Default()
	dir := t.TempDir()

	logger, path, closeLog := startLog(dir, testIdentity)

	assert.Same(t, previous, slog.Default(), "startLog must not install a process default")
	assert.Equal(t, filepath.Join(dir, testIdentity+".log"), path)
	logger.Info("explicit entry", "key", "value")
	logger.Debug("below the Info level")
	closeLog()
	logger.Info("written after closing")

	assert.Same(t, previous, slog.Default())
	assert.Regexp(t, `^time=\S+ level=INFO msg="explicit entry" key=value\n$`, readExplicitLoggerLog(t, path))
}
