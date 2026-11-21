package sharedserver

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

// UIHandlerFunc builds the handler the Web UI serves, applying maxResults as
// its ui.max_results; the handler reports every viewer heartbeat to
// heartbeat. It is called after Build has returned.
type UIHandlerFunc func(maxResults int, heartbeat func(viewerID string)) (http.Handler, error)

// uiReadHeaderTimeout bounds how long a Web UI connection may take to send
// its request headers.
const uiReadHeaderTimeout = 10 * time.Second

var errWebUIUnavailable = errors.New("web UI is not available")

// webUI is the shared server's Web UI: started by the first browse request
// that can bind, then kept until the shared server stops.
type webUI struct {
	logger  *slog.Logger
	listen  func() (net.Listener, error)
	mu      sync.Mutex
	handler UIHandlerFunc
	// heartbeat receives the viewer heartbeats of the running UI.
	heartbeat func(viewerID string)
	server    *http.Server
	url       string
	// failure is why the running UI stopped serving. The next request gets
	// it, and the one after starts the UI again.
	failure error
	closed  bool
}

func newWebUI(logger *slog.Logger, heartbeat func(viewerID string)) *webUI {
	return &webUI{logger: logger, listen: listenLoopback, heartbeat: heartbeat}
}

func listenLoopback() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}

// setHandler must be called before the first ensure.
func (u *webUI) setHandler(handler UIHandlerFunc) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.handler = handler
}

// setListen replaces how the Web UI binds; nil keeps the loopback bind. It
// must be called before the first ensure.
func (u *webUI) setListen(listen func() (net.Listener, error)) {
	if listen == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.listen = listen
}

// ensure returns the URL of the running Web UI, starting it with maxResults
// if it is not running. A failed start leaves nothing behind, so the next
// request tries again; a UI that stopped serving is reported to the next
// request in the same way.
func (u *webUI) ensure(maxResults int) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed || u.handler == nil {
		return "", errWebUIUnavailable
	}
	if u.failure != nil {
		err := u.failure
		u.failure = nil
		return "", err
	}
	if u.url != "" {
		return u.url, nil
	}
	return u.start(maxResults)
}

func (u *webUI) start(maxResults int) (string, error) {
	handler, err := u.handler(maxResults, u.heartbeat)
	if err != nil {
		return "", fmt.Errorf("failed to start the web UI: %w", err)
	}
	listener, err := u.listen()
	if err != nil {
		return "", fmt.Errorf("failed to start the web UI: %w", err)
	}
	u.server = &http.Server{Handler: handler, ReadHeaderTimeout: uiReadHeaderTimeout}
	u.url = fmt.Sprintf("http://%s/ui/", listener.Addr())
	go u.serve(u.server, listener)
	u.logger.Info("Web UI started", "url", u.url, "max_results", maxResults)
	return u.url, nil
}

func (u *webUI) serve(server *http.Server, listener net.Listener) {
	err := server.Serve(listener)
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return
	}
	u.logger.Error("Web UI stopped serving", "error", err)
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.server != server {
		return
	}
	_ = server.Close()
	u.server = nil
	u.url = ""
	u.failure = fmt.Errorf("web UI stopped serving: %w", err)
}

// close stops the Web UI for good; later requests get no UI.
func (u *webUI) close() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.closed = true
	if u.server != nil {
		_ = u.server.Close()
	}
}
