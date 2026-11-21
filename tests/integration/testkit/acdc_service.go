package testkit

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"github.com/spf13/pflag"
)

const readinessPath = "/.testkit/readiness"

type RunnerFunc func(ctx context.Context, params app.RunParams, flags *pflag.FlagSet, binary identity.Binary) error

type acdcService struct {
	name         string
	flags        *pflag.FlagSet
	errChan      chan error
	StopDelay    time.Duration
	StartTimeout time.Duration
	runner       RunnerFunc
	watchRunner  sync.Once
	watcherDone  chan struct{}
	abandonOnce  sync.Once
	abandon      chan struct{}
	runnerErr    error
	waitErr      error

	// mu orders Start's setup against Stop, so a Stop that overlaps Start sees
	// the context and pipes it must release, and a Start after Stop runs nothing.
	mu        sync.Mutex
	started   bool
	stopped   bool
	startDone chan struct{}
	startErr  error
	stopOnce  sync.Once
	stopErr   error

	// Stdio pipes
	stdinReader  *io.PipeReader
	stdinWriter  *io.PipeWriter
	stdoutReader *io.PipeReader
	stdoutWriter *io.PipeWriter
	ctxCancel    context.CancelFunc
}

// shutdownOverhead covers force-closing connections and cleanup after the app
// has waited out its full shutdown grace.
const shutdownOverhead = 5 * time.Second

func NewACDCService(name string, flags *pflag.FlagSet) Service {
	return &acdcService{
		name:         name,
		flags:        flags,
		errChan:      make(chan error, 1),
		watcherDone:  make(chan struct{}),
		abandon:      make(chan struct{}),
		startDone:    make(chan struct{}),
		StopDelay:    app.DefaultShutdownGrace + shutdownOverhead,
		StartTimeout: 10 * time.Second,
		runner:       app.RunWithDeps,
	}
}

func (s *acdcService) GetName() string {
	return s.name
}

func (s *acdcService) Start() (props map[string]any, err error) {
	transport, _ := s.flags.GetString("transport")
	ctx, err := s.claimStart(transport)
	if err != nil {
		return nil, err
	}
	defer func() {
		s.startErr = err
		close(s.startDone)
	}()

	params := app.DefaultRunParams()
	readinessMarker := rand.Text()
	if transport == "stdio" {
		params.CustomIOTransport = &mcp.IOTransport{
			Reader: s.stdinReader,
			Writer: s.stdoutWriter,
		}
	} else {
		params.StartSSEServer = func(ctx context.Context, logger *slog.Logger, mcpSrv *mcp.Server, settings *config.Settings, opts ...app.SSEOption) error {
			srv, err := app.NewSSEServer(logger, mcpSrv, settings, opts...)
			if err != nil {
				return err
			}
			srv.Handler = withReadinessMarker(srv.Handler, readinessMarker)
			return app.ServeSSEServer(ctx, logger, srv, opts...)
		}
	}

	go func() {
		err := s.runner(ctx, params, s.flags, identity.Binary{Version: "testkit"})
		s.closeStdioPipes(err)
		s.errChan <- err
	}()

	if transport == "stdio" {
		return map[string]any{
			"acdc.transport": "stdio",
			"acdc.stdin":     s.stdinWriter,
			"acdc.stdout":    s.stdoutReader,
		}, nil
	}

	return s.awaitSSEReady(readinessMarker)
}

func (s *acdcService) claimStart(transport string) (context.Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil, fmt.Errorf("%s was already started", s.name)
	}
	if s.stopped {
		return nil, fmt.Errorf("%s was stopped before it started", s.name)
	}
	s.started = true
	var ctx context.Context
	ctx, s.ctxCancel = context.WithCancel(context.Background())
	if transport == "stdio" {
		s.stdinReader, s.stdinWriter = io.Pipe()
		s.stdoutReader, s.stdoutWriter = io.Pipe()
	}
	return ctx, nil
}

func (s *acdcService) awaitSSEReady(readinessMarker string) (map[string]any, error) {
	port, _ := s.flags.GetInt("port")
	host, _ := s.flags.GetString("host")
	if host == "" || host == "0.0.0.0" {
		host = "localhost"
	}
	baseURL := fmt.Sprintf("http://%s:%d", host, port)

	deadline := time.NewTimer(s.StartTimeout)
	defer deadline.Stop()
	// The probe closes its responses unread, and the transport hands such a
	// connection back to its pool in the background. On the shared default pool
	// that races the test's first request into dialling a connection it never uses.
	client := &http.Client{
		Timeout:   100 * time.Millisecond,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	for !answersWithMarker(client, baseURL, readinessMarker) {
		select {
		case <-s.runnerExit():
			return nil, s.startupExitError()
		case <-deadline.C:
			return nil, fmt.Errorf("server failed to start after %v", s.StartTimeout)
		case <-time.After(100 * time.Millisecond):
		}
	}
	return map[string]any{
		"acdc.transport": "sse",
		"acdc.port":      port,
		"acdc.host":      host,
		"acdc.baseURL":   baseURL,
	}, nil
}

// answersWithMarker tells this start's server apart from any other listener on the
// same address: the port is chosen by closing a probe listener, so another process
// can bind it before the server does.
func answersWithMarker(client *http.Client, baseURL, marker string) bool {
	resp, err := client.Get(baseURL + readinessPath)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(len(marker))+1))
	return err == nil && resp.StatusCode == http.StatusOK && string(body) == marker
}

func withReadinessMarker(next http.Handler, marker string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == readinessPath {
			_, _ = io.WriteString(w, marker)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *acdcService) startupExitError() error {
	if s.waitErr != nil {
		return s.waitErr
	}
	if s.runnerErr == nil {
		return errors.New("server exited unexpectedly without an error before it started serving")
	}
	return fmt.Errorf("server exited unexpectedly: %w", s.runnerErr)
}

// closeStdioPipes hands the reason the server stopped to whoever is on the other end of
// the pipes. A server that failed before reaching its transport never reads stdin nor
// writes stdout, and io.Pipe has no buffer, so a client would otherwise block on its
// first write instead of learning why startup failed.
func (s *acdcService) closeStdioPipes(cause error) {
	if s.stdinReader != nil {
		_ = s.stdinReader.CloseWithError(cause)
	}
	if s.stdoutWriter != nil {
		_ = s.stdoutWriter.CloseWithError(cause)
	}
}

// Stop runs the shutdown once; every call, repeated or concurrent, returns its result.
func (s *acdcService) Stop() error {
	s.stopOnce.Do(func() { s.stopErr = s.stop() })
	return s.stopErr
}

func (s *acdcService) stop() error {
	s.mu.Lock()
	s.stopped = true
	started := s.started
	s.mu.Unlock()

	// A pooled client connection the server has not yet read a request from counts
	// as busy for its first five seconds (golang/go#22682), which would hold the
	// app's graceful shutdown for its whole grace. Tests reach the server through
	// the default transport, so its pool is emptied before the server is stopped.
	http.DefaultClient.CloseIdleConnections()
	if s.ctxCancel != nil {
		s.ctxCancel()
	}
	if s.stdinWriter != nil {
		_ = s.stdinWriter.Close()
	}
	if s.stdoutWriter != nil {
		_ = s.stdoutWriter.Close()
	}
	runnerErr := s.awaitRunner()

	if !started {
		return runnerErr
	}
	// Start settles promptly once the runner has reported or been abandoned.
	<-s.startDone
	if s.startErr == nil {
		return runnerErr
	}
	return joinStartupFailure(s.startErr, runnerErr)
}

// joinStartupFailure leaves out results the startup error already carries, such as
// the runner error that ended the start.
func joinStartupFailure(startErr error, errs ...error) error {
	joined := []error{startErr}
	for _, err := range errs {
		if err != nil && !errors.Is(startErr, err) {
			joined = append(joined, err)
		}
	}
	return errors.Join(joined...)
}

func (s *acdcService) awaitRunner() error {
	select {
	case <-s.runnerExit():
	case <-time.After(s.StopDelay):
		s.abandonOnce.Do(func() { close(s.abandon) })
		<-s.watcherDone
	}
	if s.waitErr != nil {
		return s.waitErr
	}
	if s.runnerErr != nil && s.runnerErr != http.ErrServerClosed && s.runnerErr != context.Canceled {
		return s.runnerErr
	}
	return nil
}

// runnerExit is closed once a single watcher has either received the runner's
// result from errChan or been abandoned by a Stop that gave up waiting, so Start
// and any number of Stop calls, concurrent or repeated, observe the same outcome
// and no watcher outlives a runner that never reports.
func (s *acdcService) runnerExit() <-chan struct{} {
	s.watchRunner.Do(func() {
		go func() {
			defer close(s.watcherDone)
			select {
			case s.runnerErr = <-s.errChan:
			case <-s.abandon:
				s.waitErr = fmt.Errorf("timed out waiting for %s to stop", s.name)
			}
		}()
	})
	return s.watcherDone
}
