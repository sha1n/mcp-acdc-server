//go:build unix

package sharedserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
)

// acceptWake interrupts a blocked accept when the linger expires. The woken
// flag tells a deadline set here apart from any other accept failure, and is
// cleared together with the deadline so that a later expiry is never lost.
type acceptWake struct {
	mu       sync.Mutex
	listener *net.UnixListener
	woken    bool
}

func (w *acceptWake) wake() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.woken = true
	_ = w.listener.SetDeadline(time.Now())
}

func (w *acceptWake) consume() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.woken {
		return false
	}
	w.woken = false
	_ = w.listener.SetDeadline(time.Time{})
	return true
}

// sessionAcceptor accepts from bind onward, so a session proxy that connects
// while the catalog and index are built is an attached session that waits
// for the ready line instead of a connection left in the backlog.
type sessionAcceptor struct {
	// ctx scopes accepting and the build: shutdown begins when it is
	// cancelled. sessionCtx ends the sessions, and the run context never
	// cancels it directly: it is cancelled only once accepting has stopped,
	// so a proxy that lost its session can only dial a closed listener.
	// Its Done is therefore the accepting-stopped barrier.
	ctx         context.Context
	cancel      context.CancelFunc
	sessionCtx  context.Context
	endSessions context.CancelFunc
	runCtx      context.Context
	logger      *slog.Logger
	lingeredOut bool
	listener    *net.UnixListener
	closeOnce   sync.Once
	endpoint    endpoint.Endpoint
	identity    string
	linger      *sessionLinger
	wake        *acceptWake
	built       chan struct{}
	server      *mcp.Server
	revalidate  func(context.Context)
	logPath     string
	// helloTimeout is read by every connection, so a test sets it before
	// start.
	helloTimeout time.Duration
	sessions     sync.WaitGroup
	stopped      chan struct{}
	err          error
}

func startAccepting(ctx context.Context, logger *slog.Logger, listener *net.UnixListener, ep endpoint.Endpoint, identity string, linger time.Duration) *sessionAcceptor {
	a := newSessionAcceptor(ctx, logger, listener, ep, identity, linger)
	a.start()
	return a
}

// newSessionAcceptor returns an acceptor that does not accept until start,
// so a test may change its fields first.
func newSessionAcceptor(ctx context.Context, logger *slog.Logger, listener *net.UnixListener, ep endpoint.Endpoint, identity string, linger time.Duration) *sessionAcceptor {
	a := &sessionAcceptor{
		logger:   logger,
		listener: listener,
		endpoint: ep,
		identity: identity,
		wake:     &acceptWake{listener: listener},
		built:    make(chan struct{}),
		stopped:  make(chan struct{}),

		helloTimeout: HelloTimeout,
	}
	a.runCtx = ctx
	a.ctx, a.cancel = context.WithCancel(ctx)
	a.sessionCtx, a.endSessions = context.WithCancel(context.WithoutCancel(ctx))
	a.linger = newSessionLinger(linger, realLingerClock{}, a.wake.wake)
	return a
}

func (a *sessionAcceptor) start() {
	go a.run()
}

// run is the one stop sequence for every way the shared server ends. Sessions
// end only on client close or cancellation, so they are cancelled before the
// wait; otherwise the caller keeps the lock and a dialable socket that nobody
// serves.
func (a *sessionAcceptor) run() {
	a.err = a.acceptLoop()
	a.closeListener()
	a.cancel()
	a.linger.stop()
	a.endSessions()
	a.sessions.Wait()
	close(a.stopped)
}

// closeListener is the only close of the listener. A repeated Close returns
// before the first one has released the descriptor, so a second caller waits
// for the first instead; until then a dial can still reach the socket.
func (a *sessionAcceptor) closeListener() {
	a.closeOnce.Do(func() { _ = a.listener.Close() })
}

func (a *sessionAcceptor) buildContext() context.Context {
	return a.ctx
}

// serve hands the built server to every waiting and future session and
// blocks until the shared server stops.
func (a *sessionAcceptor) serve(server *mcp.Server, revalidate func(context.Context), logPath string) error {
	a.server = server
	a.revalidate = revalidate
	a.logPath = logPath
	close(a.built)
	<-a.stopped
	return a.err
}

// abandon drops every connection after a failed build. A build that failed
// because the accept loop had already stopped reports why it stopped: an
// accept error, or nothing for a linger exit. A cancelled run context is a
// signal stop, which is normal wherever it arrives.
func (a *sessionAcceptor) abandon(buildErr error) error {
	close(a.built)
	a.cancel()
	<-a.stopped
	if a.err != nil {
		return a.err
	}
	if a.lingeredOut || a.runCtx.Err() != nil {
		return nil
	}
	return buildErr
}

func (a *sessionAcceptor) acceptLoop() error {
	stopAccepting := context.AfterFunc(a.ctx, a.closeListener)
	defer stopAccepting()

	for {
		conn, err := a.listener.AcceptUnix()
		if err == nil {
			accepted := time.Now()
			a.sessions.Go(func() { a.handle(conn, accepted) })
			continue
		}
		if a.ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, os.ErrDeadlineExceeded) && a.wake.consume() {
			if a.linger.shutdownIfExpired(a.closeAndUnlink) {
				a.logger.Info("Shared server linger expired with no session attached", "linger", a.linger.linger)
				return nil
			}
			continue
		}
		return fmt.Errorf("failed to accept shared server connection: %w", err)
	}
}

// closeAndUnlink runs while the lock is still held, so the socket it removes
// cannot belong to a successor. A connection still in the backlog is reset.
func (a *sessionAcceptor) closeAndUnlink() {
	a.closeListener()
	// A socket that cannot be removed is stale and is replaced by the next
	// lock holder at bind. Run validated the path before taking the lock.
	if socketPath, err := a.endpoint.SocketPath(a.identity); err == nil {
		_ = os.Remove(socketPath)
	}
	a.lingeredOut = true
}

// handle reads the hello before anything else, against a deadline measured
// from accept, so neither a slow build nor a pending hello can hold the
// connection open past the hello timeout. Only a connection with a valid
// hello attaches a session and so counts against the linger.
func (a *sessionAcceptor) handle(conn *net.UnixConn, accepted time.Time) {
	if !a.awaitHello(conn, accepted.Add(a.helloTimeout)) {
		_ = conn.Close()
		return
	}
	if !a.linger.attach() {
		a.dropOnceAcceptingStopped(conn)
		return
	}
	defer a.linger.detach()
	select {
	case <-a.built:
	case <-a.ctx.Done():
		a.dropOnceAcceptingStopped(conn)
		return
	}
	if a.server == nil {
		a.dropOnceAcceptingStopped(conn)
		return
	}
	attach(a.sessionCtx, a.logger, conn, a.server, a.admit, a.logPath)
}

// awaitHello reads the hello line before the session is admitted or gets its
// ready line. A connection whose hello is missing, malformed or not complete
// by deadline is refused; one cut short by shutdown is dropped silently. The
// hello deadline is set before the shutdown interrupt is installed, so it can
// never replace the immediate deadline the interrupt sets.
func (a *sessionAcceptor) awaitHello(conn *net.UnixConn, deadline time.Time) bool {
	_ = conn.SetReadDeadline(deadline)
	stopOnCancel := context.AfterFunc(a.sessionCtx, func() { _ = conn.SetReadDeadline(time.Now()) })
	_, err := ReadHelloLine(conn)
	interrupted := !stopOnCancel()
	if interrupted || a.sessionCtx.Err() != nil {
		return false
	}
	if err != nil {
		a.logger.Warn("Refused shared server connection without a valid hello line", "error", err)
		return false
	}
	_ = conn.SetReadDeadline(time.Time{})
	return true
}

// admit is the one admission decision, made after revalidation and just
// before the ready line. A shutdown that begins after it found the run
// uncancelled began after the admission, and that session is ended like any
// other attached session.
func (a *sessionAcceptor) admit(ctx context.Context) bool {
	if a.revalidate != nil && a.ctx.Err() == nil {
		a.revalidate(ctx)
	}
	if a.ctx.Err() == nil {
		return true
	}
	<-a.sessionCtx.Done()
	return false
}

// dropOnceAcceptingStopped closes a connection that gets no ready line only
// after accepting has stopped, so the peer's one retry is refused instead of
// being accepted and dropped as well.
func (a *sessionAcceptor) dropOnceAcceptingStopped(conn *net.UnixConn) {
	<-a.sessionCtx.Done()
	_ = conn.Close()
}
