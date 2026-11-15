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
	// cancels it directly: it is cancelled only after accepting has stopped,
	// so a proxy that lost its session can only dial a closed listener.
	ctx         context.Context
	cancel      context.CancelFunc
	sessionCtx  context.Context
	endSessions context.CancelFunc
	runCtx      context.Context
	logger      *slog.Logger
	lingeredOut bool
	listener    *net.UnixListener
	endpoint    endpoint.Endpoint
	identity    string
	linger      *sessionLinger
	wake        *acceptWake
	built       chan struct{}
	server      *mcp.Server
	revalidate  func(context.Context)
	logPath     string
	sessions    sync.WaitGroup
	stopped     chan struct{}
	err         error
}

func startAccepting(ctx context.Context, logger *slog.Logger, listener *net.UnixListener, ep endpoint.Endpoint, identity string, linger time.Duration) *sessionAcceptor {
	a := &sessionAcceptor{
		logger:   logger,
		listener: listener,
		endpoint: ep,
		identity: identity,
		wake:     &acceptWake{listener: listener},
		built:    make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	a.runCtx = ctx
	a.ctx, a.cancel = context.WithCancel(ctx)
	a.sessionCtx, a.endSessions = context.WithCancel(context.WithoutCancel(ctx))
	a.linger = newSessionLinger(linger, realLingerClock{}, a.wake.wake)
	go a.run()
	return a
}

// run is the one stop sequence for every way the shared server ends. Sessions
// end only on client close or cancellation, so they are cancelled before the
// wait; otherwise the caller keeps the lock and a dialable socket that nobody
// serves.
func (a *sessionAcceptor) run() {
	a.err = a.acceptLoop()
	_ = a.listener.Close()
	a.cancel()
	a.linger.stop()
	a.endSessions()
	a.sessions.Wait()
	close(a.stopped)
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
	stopAccepting := context.AfterFunc(a.ctx, func() { _ = a.listener.Close() })
	defer stopAccepting()

	for {
		conn, err := a.listener.AcceptUnix()
		if err == nil {
			a.linger.attach()
			a.sessions.Go(func() { a.handle(conn) })
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
	_ = a.listener.Close()
	// A socket that cannot be removed is stale and is replaced by the next
	// lock holder at bind. Run validated the path before taking the lock.
	if socketPath, err := a.endpoint.SocketPath(a.identity); err == nil {
		_ = os.Remove(socketPath)
	}
	a.lingeredOut = true
}

func (a *sessionAcceptor) handle(conn *net.UnixConn) {
	defer a.linger.detach()
	select {
	case <-a.built:
	case <-a.ctx.Done():
		_ = conn.Close()
		return
	}
	// Both cases can be ready at once; a connection of a shared server that
	// is shutting down must not get a ready line. It is dropped only after
	// accepting has stopped, so the peer's one retry is refused instead of
	// being accepted and dropped as well.
	if a.server == nil || a.ctx.Err() != nil {
		<-a.sessionCtx.Done()
		_ = conn.Close()
		return
	}
	attach(a.sessionCtx, a.logger, conn, a.server, a.revalidate, a.logPath)
}
