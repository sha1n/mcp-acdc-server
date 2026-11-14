//go:build unix

package testkit

import "sync"

// LaunchGate holds launches immediately before the runner starts, so that
// every contender reaches discovery only once all of them are parked.
//
// Release guarantees that each launch behind the gate has executed up to the
// gate and that none has entered the runner before the gate opens. It does
// not order the contenders after release: when each goroutine runs, and so
// how closely their discoveries overlap, is up to the Go and OS schedulers.
type LaunchGate struct {
	parked  sync.WaitGroup
	open    chan struct{}
	release sync.Once
}

// NewLaunchGate returns a gate that holds launches until Release.
func NewLaunchGate() *LaunchGate {
	return &LaunchGate{open: make(chan struct{})}
}

// LaunchBehind starts a launch like Launch whose runner starts only after
// gate is released.
func (h *SharedServerHarness) LaunchBehind(gate *LaunchGate, args ...string) *LaunchUnderTest {
	h.t.Helper()
	gate.parked.Add(1)
	return startLaunch(h.t, h.SessionProxyParams(args), h.Binary, gate, args...)
}

// Release waits until every launch behind the gate is parked at it, then
// opens it for all of them at once. Later calls return immediately.
func (g *LaunchGate) Release() {
	if g == nil {
		return
	}
	g.release.Do(func() {
		g.parked.Wait()
		close(g.open)
	})
}

func (g *LaunchGate) await() {
	if g == nil {
		return
	}
	g.parked.Done()
	<-g.open
}
