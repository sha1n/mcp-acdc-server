package sharedserver

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestViewerWindow_IsTwoMinutes(t *testing.T) {
	assert.Equal(t, 2*time.Minute, ViewerWindow)
}

func TestSessionLinger_ALiveViewerHoldsTheLingerUntilItsWindowEnds(t *testing.T) {
	p := newLingerProbe()

	p.linger.heartbeat("a")

	p.clock.advance(ViewerWindow - time.Millisecond)
	assert.Equal(t, 0, p.expired(), "a viewer heard from within the window is live")
	assert.False(t, p.shutsDown())

	p.clock.advance(time.Millisecond)
	p.clock.advance(lingerUnderTest - time.Millisecond)
	assert.Equal(t, 0, p.expired(), "the linger restarts when the viewer goes quiet")
	p.clock.advance(time.Millisecond)
	assert.Equal(t, 1, p.expired())
	assert.True(t, p.shutsDown())
}

func TestSessionLinger_EveryHeartbeatRestartsTheViewerWindow(t *testing.T) {
	p := newLingerProbe()
	p.linger.heartbeat("a")
	p.clock.advance(time.Minute)

	p.linger.heartbeat("a")

	p.clock.advance(ViewerWindow - time.Millisecond)
	assert.Equal(t, 0, p.expired())
	p.clock.advance(time.Millisecond)
	p.clock.advance(lingerUnderTest)
	assert.Equal(t, 1, p.expired())
}

func TestSessionLinger_TheLastLiveViewerStartsTheLinger(t *testing.T) {
	p := newLingerProbe()
	p.linger.heartbeat("a")
	p.clock.advance(time.Minute)
	p.linger.heartbeat("b")

	p.clock.advance(time.Minute)
	p.clock.advance(lingerUnderTest)
	assert.Equal(t, 0, p.expired(), "viewer b is still live after viewer a went quiet")

	p.clock.advance(time.Minute - lingerUnderTest)
	p.clock.advance(lingerUnderTest)
	assert.Equal(t, 1, p.expired())
}

func TestSessionLinger_AViewerOutlastingTheLastSessionDelaysTheLinger(t *testing.T) {
	p := newLingerProbe()
	p.linger.attach()
	p.linger.heartbeat("a")
	p.clock.advance(time.Second)

	p.linger.detach()

	p.clock.advance(ViewerWindow - time.Second)
	p.clock.advance(lingerUnderTest - time.Millisecond)
	assert.Equal(t, 0, p.expired(), "the linger starts only when the viewer goes quiet")
	p.clock.advance(time.Millisecond)
	assert.Equal(t, 1, p.expired())
}

func TestSessionLinger_ASessionOutlastingTheLastViewerDelaysTheLinger(t *testing.T) {
	p := newLingerProbe()
	p.linger.heartbeat("a")
	p.linger.attach()
	p.clock.advance(ViewerWindow + 10*lingerUnderTest)
	assert.Equal(t, 0, p.expired(), "an attached session holds the linger after the viewer went quiet")

	p.linger.detach()

	p.clock.advance(lingerUnderTest - time.Millisecond)
	assert.Equal(t, 0, p.expired())
	p.clock.advance(time.Millisecond)
	assert.Equal(t, 1, p.expired())
}

func TestSessionLinger_AHeartbeatRacingExpiryCancelsTheShutdown(t *testing.T) {
	p := newLingerProbe()
	p.clock.advance(lingerUnderTest)
	require.Equal(t, 1, p.expired())

	p.linger.heartbeat("a")

	assert.False(t, p.shutsDown(), "a heartbeat after expiry cancels the shutdown like an attach")
}

func TestSessionLinger_IgnoresAViewerTimerThatFiresAfterTheNextHeartbeat(t *testing.T) {
	p := newLingerProbe()
	p.linger.heartbeat("a")
	stale := p.clock.latest()
	p.clock.advance(time.Second)
	p.linger.heartbeat("a")

	stale.fireAfterLosingStop()

	p.clock.advance(lingerUnderTest)
	assert.Equal(t, 0, p.expired(), "the viewer is live until its latest window ends")
}

func TestSessionLinger_IgnoresHeartbeatsOnceStoppedOrShutDown(t *testing.T) {
	stopped := newLingerProbe()
	stopped.linger.stop()
	stopped.linger.heartbeat("a")
	assert.Equal(t, 0, stopped.clock.armed())

	shutDown := newLingerProbe()
	shutDown.clock.advance(lingerUnderTest)
	require.True(t, shutDown.shutsDown())
	shutDown.linger.heartbeat("a")
	assert.Equal(t, 0, shutDown.clock.armed())
}

func TestSessionLinger_StopDisarmsViewerWindows(t *testing.T) {
	p := newLingerProbe()
	p.linger.heartbeat("a")

	p.linger.stop()

	assert.Equal(t, 0, p.clock.armed())
}

func TestSessionLinger_UsesTheConfiguredViewerWindow(t *testing.T) {
	p := newLingerProbe()
	p.linger.setViewerWindow(time.Second)
	p.linger.heartbeat("a")

	p.clock.advance(time.Second)
	p.clock.advance(lingerUnderTest)

	assert.Equal(t, 1, p.expired())
}

func TestSessionLinger_AZeroViewerWindowKeepsTheDefault(t *testing.T) {
	p := newLingerProbe()
	p.linger.setViewerWindow(0)
	p.linger.heartbeat("a")

	p.clock.advance(time.Second + lingerUnderTest)

	assert.Equal(t, 0, p.expired())
}

func TestSessionLinger_AReleaseLeavesTheLingerStoppedWhileAViewerIsLive(t *testing.T) {
	p := newLingerProbe()
	p.linger.heartbeat("a")
	require.True(t, p.linger.hold())

	p.linger.release()

	assert.Equal(t, 1, p.clock.armed(), "only the viewer window is armed")
	p.clock.advance(ViewerWindow - time.Millisecond)
	assert.Equal(t, 0, p.expired())
}

func TestSessionLinger_ManyViewerIDsKeepOneViewerWindow(t *testing.T) {
	p := newLingerProbe()

	for i := range 1000 {
		p.linger.heartbeat(fmt.Sprintf("viewer-%d", i))
	}

	assert.Equal(t, 1, p.clock.armed(), "heartbeats share one window whatever their viewer ids")
	p.clock.advance(ViewerWindow)
	p.clock.advance(lingerUnderTest)
	assert.Equal(t, 1, p.expired())
}
