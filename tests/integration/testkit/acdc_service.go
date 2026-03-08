package testkit

import (
	"context"
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
		params.StartSSEServer = func(mcpSrv *mcp.Server, settings *config.Settings) error {
			var err error
			s.srv, err = app.NewSSEServer(mcpSrv, settings)
			if err != nil {
				return err
			}
			return s.srv.ListenAndServe()
		}
	}

	go func() {
		s.errChan <- s.runner(ctx, params, s.flags, "testkit")
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
	client := &http.Client{Timeout: 100 * time.Millisecond}
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

func (s *acdcService) Stop() error {
	if s.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), s.StopDelay)
		defer cancel()
		if err := s.srv.Shutdown(ctx); err != nil {
			return err
		}
	}

	if s.ctxCancel != nil {
		s.ctxCancel()
	}
	if s.stdinWriter != nil {
		_ = s.stdinWriter.Close()
	}
	if s.stdoutWriter != nil {
		_ = s.stdoutWriter.Close()
	}

	select {
	case err := <-s.errChan:
		if err != nil && err != http.ErrServerClosed && err != context.Canceled {
			return err
		}
	case <-time.After(s.StopDelay):
		return fmt.Errorf("timed out waiting for server to stop")
	}

	return nil
}
