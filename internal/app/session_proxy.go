package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
)

func attemptsDiscovery(logger *slog.Logger, goos string, settings *config.Settings) bool {
	return settings.Transport == "stdio" &&
		effectiveServerMode(logger, goos, settings.ServerMode) == config.ServerModeShared
}

func runSessionProxy(ctx context.Context, logger *slog.Logger, params RunParams, settings *config.Settings, binary identity.Binary) error {
	if params.EndpointDir == nil {
		return errors.New("session proxy requires an endpoint directory")
	}
	if params.Stdin == nil || params.Stdout == nil {
		return errors.New("session proxy requires host stdin and stdout")
	}
	if params.SpawnSharedServer == nil {
		return errors.New("session proxy requires a shared server spawn")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(signals)
	go func() {
		select {
		case <-signals:
			cancel()
		case <-ctx.Done():
		}
	}()

	return sessionproxy.Run(ctx, sessionproxy.Config{
		EndpointDir: params.EndpointDir,
		Identity: func() (string, error) {
			return identity.Compute(settings, binary)
		},
		ConnectWait: settings.SharedServer.ConnectWait,
		Spawn:       params.SpawnSharedServer,
		Fallback: func(ctx context.Context, input io.ReadCloser) error {
			// The per-session server gets the default signal disposition a
			// per-session launch has. Stopping notification does not cancel
			// ctx, so a signal delivered before Stop still ends the launch.
			signal.Stop(signals)
			transport := &mcp.IOTransport{Reader: input, Writer: hostOutput{params.Stdout}}
			return runPerSession(ctx, logger, params, settings, binary.Version, transport)
		},
		Logger: logger,
		Stdin:  params.Stdin,
		Stdout: params.Stdout,
	})
}
