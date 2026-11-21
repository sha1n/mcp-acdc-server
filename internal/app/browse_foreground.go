package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/identity"
	"github.com/spf13/pflag"
)

// runForegroundBrowse builds the catalog and serves the Web UI itself on a
// loopback listener until ctx is cancelled or the process is interrupted. It
// is the browse path when no shared server is available.
func runForegroundBrowse(ctx context.Context, params RunParams, logger *slog.Logger, settings *config.Settings, flags *pflag.FlagSet, binary identity.Binary) error {
	if params.CreateServer == nil {
		return errors.New("browse requires a server factory")
	}
	// A second signal must be able to kill the process: shutdown waits for
	// every handler without a time bound.
	ctx, stop := notifyShutdownSignals(ctx)
	defer stop()

	components, err := params.CreateServer(ctx, logger, withoutAuth(settings), binary.Version)
	if components.Cleanup != nil {
		defer components.Cleanup()
	}
	if ctx.Err() != nil {
		logger.Info("Browse interrupted while building the catalog")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to build the catalog: %w", err)
	}
	handler, err := newWebUIHandler(logger, components.SSEOptions, settings.UI.MaxResults, nil)
	if err != nil {
		return fmt.Errorf("failed to start the web UI: %w", err)
	}
	listener, err := listenForegroundUI(params.ListenWebUI)
	if err != nil {
		return fmt.Errorf("failed to start the web UI: %w", err)
	}
	return serveForegroundUI(ctx, logger, params, flags, handler, listener)
}

func listenForegroundUI(listen func() (net.Listener, error)) (net.Listener, error) {
	if listen != nil {
		return listen()
	}
	return net.Listen("tcp", "127.0.0.1:0")
}

func serveForegroundUI(ctx context.Context, logger *slog.Logger, params RunParams, flags *pflag.FlagSet, handler http.Handler, listener net.Listener) error {
	ui := startForegroundUI(handler, listener)
	err := awaitForegroundUI(ctx, logger, params, flags, ui, listener.Addr())
	ui.shutdown(logger, uiShutdownGrace(params))
	return err
}

func awaitForegroundUI(ctx context.Context, logger *slog.Logger, params RunParams, flags *pflag.FlagSet, ui *foregroundUI, addr net.Addr) error {
	url := fmt.Sprintf("http://%s/ui/", addr)
	logger.Info("Web UI ready", "url", url)
	if _, err := fmt.Fprintln(params.Stdout, url); err != nil {
		return err
	}
	if !noOpen(flags) {
		openBrowser(logger, params.OpenBrowser, url)
	}
	select {
	case err := <-ui.served:
		return fmt.Errorf("web UI stopped serving: %w", err)
	case <-ctx.Done():
		return nil
	}
}

// foregroundUI is a running Web UI server whose request contexts and
// in-flight handlers it controls, so shutdown can finish every handler
// before the components they use are cleaned up.
type foregroundUI struct {
	server        *http.Server
	served        chan error
	cancelRequest context.CancelFunc
	mu            sync.Mutex
	closing       bool
	handlers      sync.WaitGroup
}

func startForegroundUI(handler http.Handler, listener net.Listener) *foregroundUI {
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	ui := &foregroundUI{served: make(chan error, 1), cancelRequest: cancelRequest}
	ui.server = &http.Server{
		Handler:           ui.track(handler),
		ReadHeaderTimeout: sharedserver.UIReadHeaderTimeout,
		BaseContext:       func(net.Listener) context.Context { return requestCtx },
	}
	go func() { ui.served <- ui.server.Serve(listener) }()
	return ui
}

func (ui *foregroundUI) track(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ui.admit() {
			http.Error(w, "web UI is shutting down", http.StatusServiceUnavailable)
			return
		}
		defer ui.handlers.Done()
		next.ServeHTTP(w, r)
	})
}

// admit registers a handler unless shutdown has begun; refusing late
// requests keeps WaitGroup.Add from racing with Wait.
func (ui *foregroundUI) admit() bool {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	if ui.closing {
		return false
	}
	ui.handlers.Add(1)
	return true
}

func uiShutdownGrace(params RunParams) time.Duration {
	if params.UIShutdownGrace > 0 {
		return params.UIShutdownGrace
	}
	return DefaultShutdownGrace
}

// shutdown stops the server, ends every request context, force-closes
// connections once grace expires, and then waits for every admitted handler
// to return, however long that takes.
func (ui *foregroundUI) shutdown(logger *slog.Logger, grace time.Duration) {
	ui.mu.Lock()
	ui.closing = true
	ui.mu.Unlock()
	ui.cancelRequest()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := ui.server.Shutdown(shutdownCtx); err != nil {
		logger.Warn("Web UI shutdown grace period expired; waiting for request handlers to return")
		_ = ui.server.Close()
	}
	ui.handlers.Wait()
	logger.Info("Web UI stopped")
}
