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

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/auth"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/webui"
)

type sseOptions struct {
	uiBackend     webui.SearchBackend
	uiDocuments   webui.DocumentReader
	shutdownGrace time.Duration
	// afterInnerAccept is a test seam; see withAfterInnerAccept.
	afterInnerAccept func(net.Conn)
}

// SSEOption configures optional SSE server capabilities.
type SSEOption func(*sseOptions)

// WithWebUI supplies the components the web UI serves when it is enabled.
func WithWebUI(backend webui.SearchBackend, documents webui.DocumentReader) SSEOption {
	return func(o *sseOptions) {
		o.uiBackend = backend
		o.uiDocuments = documents
	}
}

// withShutdownGrace replaces how long StartSSEServer waits for in-flight
// handlers before force-closing their connections; zero keeps the default.
func withShutdownGrace(grace time.Duration) SSEOption {
	return func(o *sseOptions) {
		o.shutdownGrace = grace
	}
}

// withAfterInnerAccept installs a hook the listener calls on the accepting
// goroutine after the inner Accept succeeds and before the connection is
// published to the unused-connection tracker. Tests only.
func withAfterInnerAccept(hook func(net.Conn)) SSEOption {
	return func(o *sseOptions) {
		o.afterInnerAccept = hook
	}
}

func newSSEOptions(opts []SSEOption) sseOptions {
	var options sseOptions
	for _, opt := range opts {
		opt(&options)
	}
	return options
}

// mountWebUI mounts the web UI pages and API on mux; it is the one wiring of
// the web UI, for the SSE server and for a shared server's Web UI alike.
func (o sseOptions) mountWebUI(logger *slog.Logger, mux *http.ServeMux, ui config.UISettings, opts ...webui.MountOption) error {
	if o.uiBackend == nil || o.uiDocuments == nil {
		return errors.New("web UI enabled but no UI components supplied")
	}
	webui.Mount(logger, mux, o.uiBackend, o.uiDocuments, ui, opts...)
	return nil
}

// newWebUIHandler serves only the web UI pages and API, with no
// authentication and maxResults as its ui.max_results. Viewer heartbeats are
// reported to heartbeat; a nil heartbeat accepts them without effect.
func newWebUIHandler(logger *slog.Logger, opts []SSEOption, maxResults int, heartbeat webui.ViewerHeartbeat) (http.Handler, error) {
	mux := http.NewServeMux()
	ui := config.UISettings{Enabled: true, MaxResults: maxResults}
	if err := newSSEOptions(opts).mountWebUI(logger, mux, ui, webui.WithViewerHeartbeat(heartbeat)); err != nil {
		return nil, err
	}
	return mux, nil
}

// StartSSEServer starts the SSE server with authentication and serves it
// until ctx is cancelled; see ServeSSEServer.
func StartSSEServer(ctx context.Context, logger *slog.Logger, s *mcp.Server, settings *config.Settings, opts ...SSEOption) error {
	if ctx.Err() != nil {
		return nil
	}
	srv, err := NewSSEServer(logger, s, settings, opts...)
	if err != nil {
		return err
	}

	logger.Info("Server listening (HTTP)", "addr", srv.Addr, "auth_type", settings.Auth.Type)
	return ServeSSEServer(ctx, logger, srv, opts...)
}

// ServeSSEServer takes exclusive ownership of srv, which must be fresh and
// unstarted because ServeSSEServer replaces its BaseContext and ConnState and
// wraps its Handler and ConnContext, still calling the ones it was given. It
// listens on srv.Addr and serves srv until ctx is cancelled; a ctx that is
// already cancelled returns nil without listening. On cancellation it stops
// accepting, cancels every request context, gives connections that have not
// started a request a short window to send their first one, which is then
// served, ending the window early once every accepted connection has started
// a request or closed, closes those still unused, waits for in-flight handlers up to the
// shutdown grace, force-closes the connections that remain and
// returns nil once shutdown completes. Force-closing cannot stop a handler
// goroutine that ignores cancellation, so such a handler may still be running
// when ServeSSEServer returns. A listen or serve failure, including one
// observed during shutdown, is returned as an error.
func ServeSSEServer(ctx context.Context, logger *slog.Logger, srv *http.Server, opts ...SSEOption) error {
	if ctx.Err() != nil {
		return nil
	}
	inner, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("SSE server failed to listen on %s: %w", srv.Addr, err)
	}
	options := newSSEOptions(opts)
	unused := newUnusedConnCloser()
	listener := newStoppableListener(inner, unused.accepted)
	listener.afterInnerAccept = options.afterInnerAccept
	// Shutdown does not end long-lived /sse streams; cancelling their request
	// contexts does.
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	srv.BaseContext = func(net.Listener) context.Context { return requestCtx }
	unused.install(srv)

	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()
	select {
	case err := <-served:
		return fmt.Errorf("SSE server failed to serve on %s: %w", srv.Addr, err)
	case <-ctx.Done():
	}

	logger.Info("Shutting down SSE server")
	cancelRequests()
	// The window must elapse before Shutdown: once Shutdown has begun, net/http
	// drops a request whose read completes afterwards.
	windowEnd := time.Now().Add(unusedConnWindow)
	listener.stopAccepting()
	// A connection the inner listener returned before it closed may not be
	// published yet; awaitUsed would miss it and closeAll would drop it.
	listener.awaitAcceptsPublished(windowEnd)
	unused.awaitUsed(time.Until(windowEnd))
	unused.closeAll()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), options.shutdownGraceOrDefault())
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("SSE server shutdown grace period expired; closing open connections")
		_ = srv.Close()
	}
	servedErr := <-served
	logger.Info("SSE server stopped")
	if !errors.Is(servedErr, http.ErrServerClosed) {
		return fmt.Errorf("SSE server failed to serve on %s during shutdown: %w", srv.Addr, servedErr)
	}
	return nil
}

// unusedConnWindow is how long connections that have not started a request
// get, once shutdown begins, to send their first one.
const unusedConnWindow = 500 * time.Millisecond

// afterConnActive, when non-nil, is called on a connection's goroutine right
// after the tracker records http.StateActive for it; tests use it to hold the
// connection before net/http checks its shutdown flag.
var afterConnActive func()

type connKey struct{}

// unusedConnCloser closes connections that have not started a request once
// shutdown begins. http.Server.Shutdown leaves such connections open until
// they are five seconds old (golang/go#22682), so a client's spare
// pre-dialed connection would otherwise hold shutdown for the whole grace.
//
// A connection is pending from Accept until its first request enters the
// handler or it closes. StateActive alone is not enough: net/http checks its
// shutdown flag after reporting StateActive and drops the request if Shutdown
// has begun by then.
type unusedConnCloser struct {
	mu      sync.Mutex
	closing bool
	pending map[net.Conn]struct{}
	unused  map[net.Conn]struct{}
	changed chan struct{}
}

func newUnusedConnCloser() *unusedConnCloser {
	return &unusedConnCloser{
		pending: make(map[net.Conn]struct{}),
		unused:  make(map[net.Conn]struct{}),
		changed: make(chan struct{}, 1),
	}
}

// install wires the closer into srv, keeping the caller's ConnContext.
func (u *unusedConnCloser) install(srv *http.Server) {
	connContext := srv.ConnContext
	srv.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		if connContext != nil {
			ctx = connContext(ctx, c)
		}
		return context.WithValue(ctx, connKey{}, c)
	}
	srv.ConnState = u.track
	handler := srv.Handler
	if handler == nil {
		handler = http.DefaultServeMux
	}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, ok := r.Context().Value(connKey{}).(net.Conn); ok {
			u.settle(c)
		}
		handler.ServeHTTP(w, r)
	})
}

func (u *unusedConnCloser) accepted(c net.Conn) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pending[c] = struct{}{}
}

func (u *unusedConnCloser) settle(c net.Conn) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.settleLocked(c)
}

func (u *unusedConnCloser) settleLocked(c net.Conn) {
	if _, ok := u.pending[c]; !ok {
		return
	}
	delete(u.pending, c)
	select {
	case u.changed <- struct{}{}:
	default:
	}
}

func (u *unusedConnCloser) track(c net.Conn, state http.ConnState) {
	u.mu.Lock()
	switch state {
	case http.StateNew:
		if u.closing {
			u.settleLocked(c)
			_ = c.Close()
		} else {
			u.unused[c] = struct{}{}
		}
	case http.StateClosed, http.StateHijacked:
		delete(u.unused, c)
		u.settleLocked(c)
	default:
		delete(u.unused, c)
	}
	u.mu.Unlock()
	if state == http.StateActive && afterConnActive != nil {
		afterConnActive()
	}
}

// awaitUsed waits until every accepted connection has started its first
// request or closed, or window has passed.
func (u *unusedConnCloser) awaitUsed(window time.Duration) {
	timer := time.NewTimer(window)
	defer timer.Stop()
	for {
		u.mu.Lock()
		remaining := len(u.pending)
		u.mu.Unlock()
		if remaining == 0 {
			return
		}
		select {
		case <-u.changed:
		case <-timer.C:
			return
		}
	}
}

func (u *unusedConnCloser) closeAll() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.closing = true
	for c := range u.unused {
		_ = c.Close()
	}
	clear(u.unused)
}

// stoppableListener stops accepting on stopAccepting while its Accept keeps
// blocking until Close, so http.Server.Serve keeps serving the connections it
// already has instead of returning on the closed listener.
//
// It counts Accept calls from entering the inner Accept until the result is
// published or the error path is taken, so shutdown can wait until every
// connection the inner listener returned is known to onAccept.
type stoppableListener struct {
	net.Listener
	onAccept         func(net.Conn)
	afterInnerAccept func(net.Conn)
	stopOnce         sync.Once
	closeOnce        sync.Once
	drainOnce        sync.Once
	stopped          chan struct{}
	closed           chan struct{}
	drained          chan struct{}

	mu        sync.Mutex
	inFlight  int
	isStopped bool
}

func newStoppableListener(l net.Listener, onAccept func(net.Conn)) *stoppableListener {
	return &stoppableListener{
		Listener: l,
		onAccept: onAccept,
		stopped:  make(chan struct{}),
		closed:   make(chan struct{}),
		drained:  make(chan struct{}),
	}
}

func (l *stoppableListener) Accept() (net.Conn, error) {
	c, err := l.acceptAndPublish()
	if err == nil {
		return c, nil
	}
	select {
	case <-l.stopped:
		<-l.closed
	default:
	}
	return c, err
}

func (l *stoppableListener) acceptAndPublish() (net.Conn, error) {
	l.mu.Lock()
	l.inFlight++
	l.mu.Unlock()
	defer l.endAccept()
	c, err := l.Listener.Accept()
	if err != nil {
		return c, err
	}
	if l.afterInnerAccept != nil {
		l.afterInnerAccept(c)
	}
	l.onAccept(c)
	return c, nil
}

func (l *stoppableListener) endAccept() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inFlight--
	l.signalDrainedLocked()
}

func (l *stoppableListener) signalDrainedLocked() {
	if l.isStopped && l.inFlight == 0 {
		l.drainOnce.Do(func() { close(l.drained) })
	}
}

func (l *stoppableListener) stopAccepting() {
	l.stopOnce.Do(func() {
		close(l.stopped)
		_ = l.Listener.Close()
		l.mu.Lock()
		defer l.mu.Unlock()
		l.isStopped = true
		l.signalDrainedLocked()
	})
}

// awaitAcceptsPublished waits, after stopAccepting, until no Accept call is
// between entering the inner Accept and publishing its result, or until
// deadline.
func (l *stoppableListener) awaitAcceptsPublished(deadline time.Time) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-l.drained:
	case <-timer.C:
	}
}

func (l *stoppableListener) Close() error {
	l.stopAccepting()
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (o sseOptions) shutdownGraceOrDefault() time.Duration {
	if o.shutdownGrace > 0 {
		return o.shutdownGrace
	}
	return DefaultShutdownGrace
}

// NewSSEServer creates a new SSE server with authentication middleware
func NewSSEServer(logger *slog.Logger, s *mcp.Server, settings *config.Settings, opts ...SSEOption) (*http.Server, error) {
	options := newSSEOptions(opts)

	// Factory function returns the server instance for each request
	sseHandler := mcp.NewSSEHandler(func(r *http.Request) *mcp.Server {
		return s
	}, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/sse", sseHandler)
	if settings.UI.Enabled {
		if err := options.mountWebUI(logger, mux, settings.UI); err != nil {
			return nil, err
		}
	}

	authMiddleware, err := auth.NewMiddleware(settings.Auth)
	if err != nil {
		return nil, fmt.Errorf("failed to create auth middleware: %w", err)
	}

	handler := authMiddleware(mux)
	addr := fmt.Sprintf("%s:%d", settings.Host, settings.Port)

	return &http.Server{
		Addr:    addr,
		Handler: handler,
	}, nil
}
