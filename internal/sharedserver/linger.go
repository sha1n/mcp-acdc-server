package sharedserver

import (
	"sync"
	"time"
)

// ViewerWindow is how long a viewer counts as live after its last
// heartbeat. It is longer than the page's heartbeat interval because
// browsers slow timers in background tabs.
const ViewerWindow = 2 * time.Minute

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

// sessionLinger counts attached sessions, pending holds and live viewers,
// and decides when the shared server has had none of them for the whole
// linger. Expiry only marks the linger as expired and calls onExpire; the
// shutdown itself is confirmed by shutdownIfExpired, so an attach between
// the two cancels it.
type sessionLinger struct {
	mu       sync.Mutex
	clock    lingerClock
	linger   time.Duration
	onExpire func()
	sessions int
	// holds counts pending requests, such as a browse, that are not
	// sessions. Like a session, a hold stops the linger, and the last
	// release restarts it, so the page a browse opened gets a whole linger
	// to send its first heartbeat.
	holds int
	// viewers is the one window shared by every viewer: it ends a viewer
	// window after the latest heartbeat of any viewer, so the state does not
	// grow with the number of viewer ids.
	viewers      lingerWindow
	viewerWindow time.Duration
	countdown    lingerWindow
	expired      bool
	shutDown     bool
	stopped      bool
}

// lingerWindow is one timer whose generation identifies the armed callback,
// so a callback that fires after Stop lost the race to it is ignored.
type lingerWindow struct {
	timer      lingerTimer
	generation uint64
}

func (w *lingerWindow) armed() bool {
	return w.timer != nil
}

func (w *lingerWindow) start(clock lingerClock, d time.Duration, f func(generation uint64)) {
	w.stop()
	generation := w.generation
	w.timer = clock.AfterFunc(d, func() { f(generation) })
}

func (w *lingerWindow) stop() {
	w.generation++
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
}

// current reports whether generation is that of the armed timer, and
// clears the timer if it is, since it has fired.
func (w *lingerWindow) current(generation uint64) bool {
	if generation != w.generation || w.timer == nil {
		return false
	}
	w.timer = nil
	return true
}

// newSessionLinger starts the timer at once: it is created at bind, which
// covers a candidate whose spawning session proxy never connects.
func newSessionLinger(linger time.Duration, clock lingerClock, onExpire func()) *sessionLinger {
	l := &sessionLinger{clock: clock, linger: linger, onExpire: onExpire, viewerWindow: ViewerWindow}
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
	if !l.occupy() {
		return false
	}
	l.sessions++
	return true
}

// hold stops the linger until release, as an attach does, without counting
// a session. It reports false, like attach, once the linger has shut the
// shared server down or stopped.
func (l *sessionLinger) hold() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.occupy() {
		return false
	}
	l.holds++
	return true
}

// release ends a hold. The last release with no session attached and no
// viewer live restarts the linger, as the last detach does.
func (l *sessionLinger) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.holds--
	l.armIfVacant()
}

func (l *sessionLinger) detach() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sessions--
	l.armIfVacant()
}

// setViewerWindow replaces how long a viewer stays live after a heartbeat;
// zero keeps ViewerWindow.
func (l *sessionLinger) setViewerWindow(window time.Duration) {
	if window <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.viewerWindow = window
}

// heartbeat keeps a viewer live for the viewer window from now. Every viewer
// shares one window, since the linger depends only on whether any viewer is
// live, so viewerID is not tracked. A heartbeat with no viewer live stops
// the linger as an attach does. Once the linger has shut the shared server
// down, or it has stopped, it is ignored.
func (l *sessionLinger) heartbeat(_ string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.shutDown || l.stopped {
		return
	}
	if !l.viewers.armed() {
		l.occupy()
	}
	l.viewers.start(l.clock, l.viewerWindow, l.viewersQuiet)
}

func (l *sessionLinger) viewersQuiet(generation uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.viewers.current(generation) {
		l.armIfVacant()
	}
}

// occupy stops the linger for a session, hold or viewer that is about to
// count, and reports false once the linger has shut the shared server down
// or stopped.
func (l *sessionLinger) occupy() bool {
	if l.shutDown || l.stopped {
		return false
	}
	l.expired = false
	l.countdown.stop()
	return true
}

// vacant reports whether no session is attached, no hold is pending and no
// viewer is live.
func (l *sessionLinger) vacant() bool {
	return l.sessions == 0 && l.holds == 0 && !l.viewers.armed()
}

// armIfVacant restarts the linger when the last session, hold or viewer is
// gone.
func (l *sessionLinger) armIfVacant() {
	if l.vacant() && !l.stopped && !l.shutDown {
		l.arm()
	}
}

// shutdownIfExpired runs shutdown under the session-count lock when the
// linger has expired with no session attached, no hold pending and no
// viewer live, and reports whether it ran.
func (l *sessionLinger) shutdownIfExpired(shutdown func()) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.expired || !l.vacant() {
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
	l.countdown.stop()
	l.viewers.stop()
}

func (l *sessionLinger) arm() {
	l.countdown.start(l.clock, l.linger, l.expire)
}

func (l *sessionLinger) expire(generation uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.countdown.current(generation) {
		return
	}
	l.expired = true
	l.onExpire()
}
