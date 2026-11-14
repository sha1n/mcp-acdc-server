//go:build unix

package sharedserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	ctx            context.Context
	abandonCtx     context.CancelFunc
	buildCtx       context.Context
	cancelBuild    context.CancelFunc
	lingeredOut    bool
	listener       *net.UnixListener
	socketPath     string
	linger         *sessionLinger
	wake           *acceptWake
	built          chan struct{}
	server         *mcp.Server
	revalidate     func(context.Context)
	logPath        string
	sessionCtx     context.Context
	cancelSessions context.CancelFunc
	sessions       sync.WaitGroup
	stopped        chan struct{}
	err            error
}

func startAccepting(ctx context.Context, listener *net.UnixListener, socketPath string, linger time.Duration) *sessionAcceptor {
	a := &sessionAcceptor{
		listener:   listener,
		socketPath: socketPath,
		wake:       &acceptWake{listener: listener},
		built:      make(chan struct{}),
		stopped:    make(chan struct{}),
	}
	a.ctx, a.abandonCtx = context.WithCancel(ctx)
	a.buildCtx, a.cancelBuild = context.WithCancel(ctx)
	a.sessionCtx, a.cancelSessions = context.WithCancel(a.ctx)
	a.linger = newSessionLinger(linger, realLingerClock{}, a.wake.wake)
	go func() {
		a.err = a.acceptLoop()
		close(a.stopped)
	}()
	return a
}

// serve hands the built server to every waiting and future session and
// blocks until the shared server stops.
func (a *sessionAcceptor) serve(server *mcp.Server, revalidate func(context.Context), logPath string) error {
	a.server = server
	a.revalidate = revalidate
	a.logPath = logPath
	close(a.built)
	<-a.stopped
	a.cancelBuild()
	return a.err
}

// abandon drops every connection after a failed build and returns buildErr,
// unless the build failed because the linger expired first: that is a
// normal exit.
func (a *sessionAcceptor) abandon(buildErr error) error {
	close(a.built)
	a.abandonCtx()
	<-a.stopped
	a.cancelBuild()
	if a.lingeredOut {
		return nil
	}
	return buildErr
}

func (a *sessionAcceptor) acceptLoop() error {
	stopAccepting := context.AfterFunc(a.ctx, func() { _ = a.listener.Close() })
	defer stopAccepting()

	// Sessions end only on client close or cancellation, so whatever stops the
	// accept loop must close the listener and cancel the sessions before
	// waiting; otherwise the caller keeps the lock and a dialable socket that
	// nobody serves.
	defer func() {
		_ = a.listener.Close()
		a.linger.stop()
		a.cancelSessions()
		a.sessions.Wait()
	}()

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
	// lock holder at bind.
	_ = os.Remove(a.socketPath)
	a.lingeredOut = true
	a.cancelBuild()
}

func (a *sessionAcceptor) handle(conn *net.UnixConn) {
	defer a.linger.detach()
	select {
	case <-a.built:
	case <-a.sessionCtx.Done():
		_ = conn.Close()
		return
	}
	if a.server == nil {
		_ = conn.Close()
		return
	}
	attach(a.sessionCtx, conn, a.server, a.revalidate, a.logPath)
}
