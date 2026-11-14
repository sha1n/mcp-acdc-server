package sharedserver

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const lingerUnderTest = 10 * time.Second

type fakeLingerClock struct {
	mu     sync.Mutex
	now    time.Duration
	timers []*fakeLingerTimer
}

type fakeLingerTimer struct {
	clock   *fakeLingerClock
	at      time.Duration
	f       func()
	stopped bool
	fired   bool
}

func (c *fakeLingerClock) AfterFunc(d time.Duration, f func()) lingerTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeLingerTimer{clock: c, at: c.now + d, f: f}
	c.timers = append(c.timers, timer)
	return timer
}

func (c *fakeLingerClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now += d
	var due []*fakeLingerTimer
	for _, timer := range c.timers {
		if !timer.stopped && !timer.fired && timer.at <= c.now {
			timer.fired = true
			due = append(due, timer)
		}
	}
	c.mu.Unlock()
	for _, timer := range due {
		timer.f()
	}
}

func (c *fakeLingerClock) armed() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	armed := 0
	for _, timer := range c.timers {
		if !timer.stopped && !timer.fired {
			armed++
		}
	}
	return armed
}

func (c *fakeLingerClock) latest() *fakeLingerTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.timers[len(c.timers)-1]
}

func (t *fakeLingerTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	return true
}

// fireAfterLosingStop runs the callback of a timer whose Stop has already
// been called, as a real timer does when Stop races its expiry.
func (t *fakeLingerTimer) fireAfterLosingStop() {
	t.f()
}

type lingerProbe struct {
	clock    *fakeLingerClock
	linger   *sessionLinger
	mu       sync.Mutex
	expiries int
}

func newLingerProbe() *lingerProbe {
	p := &lingerProbe{clock: &fakeLingerClock{}}
	p.linger = newSessionLinger(lingerUnderTest, p.clock, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.expiries++
	})
	return p
}

func (p *lingerProbe) expired() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.expiries
}

func (p *lingerProbe) shutsDown() bool {
	return p.linger.shutdownIfExpired(func() {})
}

func TestSessionLinger_StartsAtCreationAndExpiresAfterTheLinger(t *testing.T) {
	p := newLingerProbe()
	require.Equal(t, 1, p.clock.armed(), "the timer starts when the linger is created, at bind")

	p.clock.advance(lingerUnderTest - time.Millisecond)
	assert.Equal(t, 0, p.expired())
	assert.False(t, p.shutsDown(), "no shutdown before the linger has elapsed")

	p.clock.advance(time.Millisecond)
	assert.Equal(t, 1, p.expired())
	assert.True(t, p.shutsDown())
}

func TestSessionLinger_AttachCancelsTheTimer(t *testing.T) {
	p := newLingerProbe()
	p.clock.advance(lingerUnderTest / 2)

	p.linger.attach()

	assert.Equal(t, 0, p.clock.armed())
	p.clock.advance(10 * lingerUnderTest)
	assert.Equal(t, 0, p.expired())
	assert.False(t, p.shutsDown())
}

func TestSessionLinger_RestartsOnlyAtTheLastDetach(t *testing.T) {
	p := newLingerProbe()
	p.linger.attach()
	p.linger.attach()

	p.linger.detach()
	assert.Equal(t, 0, p.clock.armed(), "a session is still attached")
	p.clock.advance(10 * lingerUnderTest)
	assert.Equal(t, 0, p.expired())

	p.linger.detach()
	assert.Equal(t, 1, p.clock.armed(), "the last detach restarts the timer")
	p.clock.advance(lingerUnderTest - time.Millisecond)
	assert.Equal(t, 0, p.expired())
	p.clock.advance(time.Millisecond)
	assert.Equal(t, 1, p.expired())
	assert.True(t, p.shutsDown())
}

func TestSessionLinger_FiresOnlyAfterAContinuousZeroPeriod(t *testing.T) {
	p := newLingerProbe()
	p.clock.advance(lingerUnderTest / 2)
	p.linger.attach()
	p.clock.advance(time.Second)
	p.linger.detach()

	p.clock.advance(lingerUnderTest - time.Second)
	assert.Equal(t, 0, p.expired(), "the zero period before the attach must not count")

	p.clock.advance(time.Second)
	assert.Equal(t, 1, p.expired())
}

func TestSessionLinger_AnAcceptRacingExpiryCancelsTheShutdown(t *testing.T) {
	p := newLingerProbe()
	p.clock.advance(lingerUnderTest)
	require.Equal(t, 1, p.expired())

	p.linger.attach()

	shutdowns := 0
	assert.False(t, p.linger.shutdownIfExpired(func() { shutdowns++ }))
	assert.Equal(t, 0, shutdowns, "a connection accepted after expiry cancels the shutdown")

	p.linger.detach()
	p.clock.advance(lingerUnderTest)
	assert.True(t, p.linger.shutdownIfExpired(func() { shutdowns++ }))
	assert.Equal(t, 1, shutdowns)
}

func TestSessionLinger_IgnoresATimerThatFiresAfterAnAttach(t *testing.T) {
	p := newLingerProbe()
	timer := p.clock.latest()

	p.linger.attach()
	timer.fireAfterLosingStop()

	assert.Equal(t, 0, p.expired())
	assert.False(t, p.shutsDown())
}

func TestSessionLinger_ShutdownRunsUnderTheSessionCountLock(t *testing.T) {
	p := newLingerProbe()
	p.clock.advance(lingerUnderTest)

	attached := make(chan struct{})
	var attachedDuringShutdown bool
	shutDown := p.linger.shutdownIfExpired(func() {
		go func() {
			p.linger.attach()
			close(attached)
		}()
		select {
		case <-attached:
			attachedDuringShutdown = true
		case <-time.After(50 * time.Millisecond):
		}
	})

	require.True(t, shutDown)
	assert.False(t, attachedDuringShutdown, "an attach must wait for the shutdown to finish")
	<-attached
}

func TestSessionLinger_StopDisarmsTheTimerForGood(t *testing.T) {
	p := newLingerProbe()
	p.linger.attach()

	p.linger.stop()
	p.linger.detach()

	assert.Equal(t, 0, p.clock.armed())
	p.clock.advance(10 * lingerUnderTest)
	assert.Equal(t, 0, p.expired())
}

func TestSessionLinger_StopCancelsAPendingTimer(t *testing.T) {
	p := newLingerProbe()

	p.linger.stop()

	assert.Equal(t, 0, p.clock.armed())
	p.clock.advance(10 * lingerUnderTest)
	assert.Equal(t, 0, p.expired())
}
