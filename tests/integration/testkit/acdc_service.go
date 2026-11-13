package testkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/spf13/pflag"
)

type RunnerFunc func(ctx context.Context, params app.RunParams, flags *pflag.FlagSet, version string) error

type acdcService struct {
	name         string
	flags        *pflag.FlagSet
	srv          *http.Server
	errChan      chan error
	StopDelay    time.Duration
	StartTimeout time.Duration
	runner       RunnerFunc

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
		// For SSE, use custom handler that captures server instance
		params.StartSSEServer = func(mcpSrv *mcp.Server, settings *config.Settings, opts ...app.SSEOption) error {
			var err error
			s.srv, err = app.NewSSEServer(mcpSrv, settings, opts...)
			if err != nil {
				return err
			}
			return s.srv.ListenAndServe()
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

	// Wait for server to start by polling /sse
	port, _ := s.flags.GetInt("port")
	host, _ := s.flags.GetString("host")
	if host == "" || host == "0.0.0.0" {
		host = "localhost"
	}
	baseURL := fmt.Sprintf("http://%s:%d", host, port)

	deadline := time.Now().Add(s.StartTimeout)
	// The probe closes its responses unread, and the transport hands such a
	// connection back to its pool in the background. On the shared default pool
	// that races the test's first request into dialling a connection it never uses.
	client := &http.Client{
		Timeout:   100 * time.Millisecond,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	for time.Now().Before(deadline) {
		select {
		case err := <-s.errChan:
			return nil, fmt.Errorf("server exited unexpectedly: %w", err)
		default:
			resp, err := client.Get(baseURL + "/sse")
			if err == nil {
				_ = resp.Body.Close()
				return map[string]any{
					"acdc.transport": "sse",
					"acdc.port":      port,
					"acdc.host":      host,
					"acdc.baseURL":   baseURL,
				}, nil
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	return nil, fmt.Errorf("server failed to start after %v", s.StartTimeout)
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
	if s.srv == nil {
		return nil
	}

	// A pooled client connection the server has not yet read a request from counts
	// as busy for its first five seconds (golang/go#22682), which would hold the
	// graceful shutdown for the whole StopDelay. Tests reach the server through the
	// default transport, so its pool is emptied before the server is asked to stop.
	http.DefaultClient.CloseIdleConnections()

	ctx, cancel := context.WithTimeout(context.Background(), s.StopDelay)
	defer cancel()
	if err := s.srv.Shutdown(ctx); err != nil {
		// Shutdown only stops accepting; without this the connections it gave up
		// on, and the goroutines serving them, would outlive the test.
		_ = s.srv.Close()
		return fmt.Errorf("graceful shutdown of %s failed, connections were closed forcibly: %w", s.name, err)
	}
	return nil
}

func (s *acdcService) awaitRunner() error {
	select {
	case err := <-s.errChan:
		if err != nil && err != http.ErrServerClosed && err != context.Canceled {
			return err
		}
		return nil
	case <-time.After(s.StopDelay):
		return fmt.Errorf("timed out waiting for server to stop")
	}
}
