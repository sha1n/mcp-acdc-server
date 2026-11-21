package sharedserver

import (
	"sync"
	"time"
)

type lingerClock interface {
	AfterFunc(d time.Duration, f func()) lingerTimer
}

type lingerTimer interface {
	Stop() bool
}

type realLingerClock struct{}

func (realLingerClock) AfterFunc(d time.Duration, f func()) lingerTimer {
	return time.AfterFunc(d, f)
}

// sessionLinger counts attached sessions and decides when the shared server
// has had none for the whole linger. Expiry only marks the linger as expired
// and calls onExpire; the shutdown itself is confirmed by shutdownIfExpired,
// so an attach between the two cancels it.
type sessionLinger struct {
	mu       sync.Mutex
	clock    lingerClock
	linger   time.Duration
	onExpire func()
	sessions int
	// holds counts pending requests, such as a browse, that are not
	// sessions: they defer a shutdown but never touch the timer.
	holds int
	timer lingerTimer
	// generation identifies the armed timer, so a callback that fires after
	// Stop lost the race to it is ignored.
	generation uint64
	expired    bool
	shutDown   bool
	stopped    bool
}

// newSessionLinger starts the timer at once: it is created at bind, which
// covers a candidate whose spawning session proxy never connects.
func newSessionLinger(linger time.Duration, clock lingerClock, onExpire func()) *sessionLinger {
	l := &sessionLinger{clock: clock, linger: linger, onExpire: onExpire}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.arm()
	return l
}

// attach counts a session and reports whether it may proceed. Once the
// linger has shut the shared server down, or it has stopped, no session is
// counted: the caller must drop the connection.
func (l *sessionLinger) attach() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.shutDown || l.stopped {
		return false
	}
	l.sessions++
	l.expired = false
	l.disarm()
	return true
}

// hold defers the shutdown of an expired linger until release, without
// resetting or extending a running linger. It reports false, like attach,
// once the linger has shut the shared server down or stopped.
func (l *sessionLinger) hold() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.shutDown || l.stopped {
		return false
	}
	l.holds++
	return true
}

// release ends a hold. The last release after an expiry reports the expiry
// again, since the shutdown it deferred is still due.
func (l *sessionLinger) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.holds--
	if l.holds == 0 && l.expired && l.sessions == 0 && !l.stopped {
		l.onExpire()
	}
}

func (l *sessionLinger) detach() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sessions--
	if l.sessions == 0 && !l.stopped {
		l.arm()
	}
}

// shutdownIfExpired runs shutdown under the session-count lock when the
// linger has expired with no session attached and no hold pending, and
// reports whether it ran.
func (l *sessionLinger) shutdownIfExpired(shutdown func()) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.expired || l.sessions != 0 || l.holds != 0 {
		return false
	}
	shutdown()
	l.shutDown = true
	return true
}

func (l *sessionLinger) stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopped = true
	l.disarm()
}

func (l *sessionLinger) arm() {
	l.generation++
	generation := l.generation
	l.timer = l.clock.AfterFunc(l.linger, func() { l.expire(generation) })
}

func (l *sessionLinger) disarm() {
	l.generation++
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
}

func (l *sessionLinger) expire(generation uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if generation != l.generation {
		return
	}
	l.expired = true
	l.onExpire()
}
