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
	// ctx scopes the build and every session. It is cancelled whenever the
	// accept loop stops, whatever the reason, so that neither outlives it.
	ctx         context.Context
	cancel      context.CancelFunc
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

func startAccepting(ctx context.Context, listener *net.UnixListener, ep endpoint.Endpoint, identity string, linger time.Duration) *sessionAcceptor {
	a := &sessionAcceptor{
		listener: listener,
		endpoint: ep,
		identity: identity,
		wake:     &acceptWake{listener: listener},
		built:    make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	a.ctx, a.cancel = context.WithCancel(ctx)
	a.linger = newSessionLinger(linger, realLingerClock{}, a.wake.wake)
	go func() {
		a.err = a.acceptLoop()
		close(a.stopped)
	}()
	return a
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
// accept error, or nothing for a linger exit, which is a normal exit.
func (a *sessionAcceptor) abandon(buildErr error) error {
	close(a.built)
	a.cancel()
	<-a.stopped
	if a.err != nil {
		return a.err
	}
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
		a.cancel()
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
	// Both cases can be ready at once; a session already cancelled must not
	// get a ready line.
	if a.server == nil || a.ctx.Err() != nil {
		_ = conn.Close()
		return
	}
	attach(a.ctx, conn, a.server, a.revalidate, a.logPath)
}
