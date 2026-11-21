package app

import (
	"net"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chanListener is a net.Listener whose Accept blocks on channels, so a
// synctest bubble sees it as durably blocked (real TCP Accept is not).
type chanListener struct {
	conns     chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
}

func newChanListener() *chanListener {
	return &chanListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *chanListener) Addr() net.Addr { return &net.TCPAddr{} }

type fakeConn struct{ net.Conn }

// heldAcceptFixture is a stoppableListener whose single Accept has returned
// a connection from the inner listener and is held before publication.
type heldAcceptFixture struct {
	listener  *stoppableListener
	conn      net.Conn
	release   chan struct{}
	published chan net.Conn
	acceptRet chan struct{}
}

func startHeldAccept(t *testing.T) *heldAcceptFixture {
	t.Helper()
	inner := newChanListener()
	f := &heldAcceptFixture{
		conn:      &fakeConn{},
		release:   make(chan struct{}),
		published: make(chan net.Conn, 1),
		acceptRet: make(chan struct{}),
	}
	f.listener = newStoppableListener(inner, func(c net.Conn) { f.published <- c })
	f.listener.afterInnerAccept = func(net.Conn) { <-f.release }

	go func() {
		defer close(f.acceptRet)
		_, _ = f.listener.Accept()
	}()
	inner.conns <- f.conn
	synctest.Wait()
	return f
}

func startAwait(l *stoppableListener, deadline time.Time) <-chan time.Time {
	returned := make(chan time.Time, 1)
	go func() {
		l.awaitAcceptsPublished(deadline)
		returned <- time.Now()
	}()
	return returned
}

func TestAwaitAcceptsPublished_WaitsForHeldAcceptToPublish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := startHeldAccept(t)
		deadline := time.Now().Add(time.Second)

		f.listener.stopAccepting()
		returned := startAwait(f.listener, deadline)
		synctest.Wait()

		select {
		case <-returned:
			t.Fatal("awaitAcceptsPublished returned while an accepted connection was unpublished")
		default:
		}
		assert.Empty(t, f.published, "onAccept ran before release")

		close(f.release)
		synctest.Wait()

		select {
		case at := <-returned:
			assert.True(t, at.Before(deadline), "returned at %v, deadline %v", at, deadline)
		default:
			t.Fatal("awaitAcceptsPublished did not return after publication")
		}
		require.Len(t, f.published, 1)
		assert.Same(t, f.conn, <-f.published)
		<-f.acceptRet
		require.NoError(t, f.listener.Close())
	})
}

func TestAwaitAcceptsPublished_ReturnsImmediatelyWithNoAcceptInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newStoppableListener(newChanListener(), func(net.Conn) {
			t.Error("onAccept called without an accepted connection")
		})
		start := time.Now()

		l.stopAccepting()
		l.awaitAcceptsPublished(start.Add(time.Second))

		assert.WithinDuration(t, start, time.Now(), 0, "fake time advanced while waiting")
		require.NoError(t, l.Close())
	})
}

func TestAwaitAcceptsPublished_ReturnsAtDeadlineWhileAcceptHeld(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := startHeldAccept(t)
		deadline := time.Now().Add(time.Second)

		f.listener.stopAccepting()
		at := <-startAwait(f.listener, deadline)

		assert.WithinDuration(t, deadline, at, 0)
		assert.Empty(t, f.published, "onAccept ran before release")

		close(f.release)
		<-f.acceptRet
		assert.Same(t, f.conn, <-f.published)
		require.NoError(t, f.listener.Close())
	})
}
