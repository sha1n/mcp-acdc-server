package testkit

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/spf13/pflag"
)

const readinessPath = "/.testkit/readiness"

type RunnerFunc func(ctx context.Context, params app.RunParams, flags *pflag.FlagSet, version string) error

type acdcService struct {
	name         string
	flags        *pflag.FlagSet
	srv          atomic.Pointer[http.Server]
	errChan      chan error
	StopDelay    time.Duration
	StartTimeout time.Duration
	runner       RunnerFunc
	runnerExited bool
	runnerErr    error

	// Stdio pipes
	stdinReader  *io.PipeReader
	stdinWriter  *io.PipeWriter
	stdoutReader *io.PipeReader
	stdoutWriter *io.PipeWriter
	ctxCancel    context.CancelFunc
}

func NewACDCService(name string, flags *pflag.FlagSet) Service {
	return &acdcService{
		name:         name,
		flags:        flags,
		errChan:      make(chan error, 1),
		StopDelay:    5 * time.Second,
		StartTimeout: 10 * time.Second,
		runner:       app.RunWithDeps,
	}
}

func (s *acdcService) GetName() string {
	return s.name
}

func (s *acdcService) Start() (map[string]any, error) {
	params := app.DefaultRunParams()
	readinessMarker := rand.Text()
	transport, _ := s.flags.GetString("transport")

	var ctx context.Context
	ctx, s.ctxCancel = context.WithCancel(context.Background())

	if transport == "stdio" {
		// Create pipes for stdio testing
		s.stdinReader, s.stdinWriter = io.Pipe()
		s.stdoutReader, s.stdoutWriter = io.Pipe()

		// Create custom IO transport for testing
		params.CustomIOTransport = &mcp.IOTransport{
			Reader: s.stdinReader,
			Writer: s.stdoutWriter,
		}
	} else {
		params.StartSSEServer = func(logger *slog.Logger, mcpSrv *mcp.Server, settings *config.Settings, opts ...app.SSEOption) error {
			srv, err := app.NewSSEServer(logger, mcpSrv, settings, opts...)
			if err != nil {
				return err
			}
			srv.Handler = withReadinessMarker(srv.Handler, readinessMarker)
			s.srv.Store(srv)
			return srv.ListenAndServe()
		}
	}

	go func() {
		err := s.runner(ctx, params, s.flags, "testkit")
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
		case err := <-s.errChan:
			s.recordRunnerExit(err)
			return nil, startupExitError(err)
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

func startupExitError(err error) error {
	if err == nil {
		return errors.New("server exited unexpectedly without an error before it started serving")
	}
	return fmt.Errorf("server exited unexpectedly: %w", err)
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

func (s *acdcService) Stop() error {
	shutdownErr := s.shutdownHTTPServer()

	if s.ctxCancel != nil {
		s.ctxCancel()
	}
	if s.stdinWriter != nil {
		_ = s.stdinWriter.Close()
	}
	if s.stdoutWriter != nil {
		_ = s.stdoutWriter.Close()
	}

	return errors.Join(shutdownErr, s.awaitRunner())
}

func (s *acdcService) shutdownHTTPServer() error {
	srv := s.srv.Load()
	if srv == nil {
		return nil
	}

	// A pooled client connection the server has not yet read a request from counts
	// as busy for its first five seconds (golang/go#22682), which would hold the
	// graceful shutdown for the whole StopDelay. Tests reach the server through the
	// default transport, so its pool is emptied before the server is asked to stop.
	http.DefaultClient.CloseIdleConnections()

	ctx, cancel := context.WithTimeout(context.Background(), s.StopDelay)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		// Shutdown only stops accepting; without this the connections it gave up
		// on, and the goroutines serving them, would outlive the test.
		_ = srv.Close()
		return fmt.Errorf("graceful shutdown of %s failed, connections were closed forcibly: %w", s.name, err)
	}
	return nil
}

func (s *acdcService) awaitRunner() error {
	if !s.runnerExited {
		select {
		case err := <-s.errChan:
			s.recordRunnerExit(err)
		case <-time.After(s.StopDelay):
			return fmt.Errorf("timed out waiting for server to stop")
		}
	}
	if s.runnerErr != nil && s.runnerErr != http.ErrServerClosed && s.runnerErr != context.Canceled {
		return s.runnerErr
	}
	return nil
}

// recordRunnerExit keeps the runner's single result for every later Stop, since
// errChan delivers it only once and Start may already have received it.
func (s *acdcService) recordRunnerExit(err error) {
	s.runnerExited = true
	s.runnerErr = err
}
