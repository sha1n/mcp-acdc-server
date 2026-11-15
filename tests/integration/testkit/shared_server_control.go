//go:build unix

package testkit

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HeldSearchQuery is the search query that a shared server of a harness
// with HoldSearches receives but never answers.
const HeldSearchQuery = "held-by-the-harness"

// heldSearchBacklog bounds how many held searches may be received before the
// test awaits them.
const heldSearchBacklog = 64

type runningCandidate struct {
	cancel context.CancelFunc
	end    *candidateEnd
}

var errHarnessStopped = errors.New("the shared server harness has stopped: no candidate can be spawned")

// candidateSet is the one owner of the candidates a harness started. The spawn
// seam stays callable after the harness cleanup began, because a session
// proxy abandons a supervised spawn when its session ends; once stopped, the
// set refuses every start, so no start can race the wait for the started ones.
type candidateSet struct {
	mu      sync.Mutex
	stopped bool
	running []*runningCandidate
	ended   sync.WaitGroup
}

func (s *candidateSet) start(c *runningCandidate, run func()) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return errHarnessStopped
	}
	s.running = append(s.running, c)
	s.ended.Go(run)
	return nil
}

func (s *candidateSet) takeRunning() []*runningCandidate {
	s.mu.Lock()
	defer s.mu.Unlock()
	running := s.running
	s.running = nil
	return running
}

func (s *candidateSet) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
}

// awaitEnded must follow stop: only then has every start that will ever
// succeed already happened.
func (s *candidateSet) awaitEnded() { s.ended.Wait() }

// StopSharedServers ends every shared server candidate spawned so far by
// cancelling its goroutine (spec 13.3) and returns once each has returned,
// so every session of those servers has lost its connection by then. Later
// spawns are not affected. The test fails if one has not returned within
// SharedServerDeadline.
func (h *SharedServerHarness) StopSharedServers() {
	h.t.Helper()
	stopping := h.candidates.takeRunning()

	deadline := time.After(SharedServerDeadline)
	for _, c := range stopping {
		c.cancel()
	}
	for _, c := range stopping {
		select {
		case <-c.end.ended:
		case <-deadline:
			h.t.Fatalf("a cancelled shared server did not return within %s", SharedServerDeadline)
		}
	}
}

// HeldSearches is how a test observes that a shared server received a search
// for HeldSearchQuery, which it holds unanswered until its session ends.
type HeldSearches struct {
	h        *SharedServerHarness
	received chan struct{}
}

// HoldSearches makes every shared server h builds hold each search for
// HeldSearchQuery unanswered. It must be called before the first spawn.
func (h *SharedServerHarness) HoldSearches() *HeldSearches {
	h.t.Helper()
	h.requireNoSpawn("HoldSearches")
	held := &HeldSearches{h: h, received: make(chan struct{}, heldSearchBacklog)}
	h.receiving = append(h.receiving, held.middleware)
	return held
}

// AwaitReceived returns once a shared server has received one more held
// search than earlier calls returned for: its request line was read whole
// from the session's socket. The test fails if none arrives within
// SharedServerDeadline.
func (s *HeldSearches) AwaitReceived() {
	s.h.t.Helper()
	select {
	case <-s.received:
	case <-time.After(SharedServerDeadline):
		s.h.t.Fatalf("no held search was received within %s", SharedServerDeadline)
	}
}

func (s *HeldSearches) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if !isHeldSearch(method, req) {
			return next(ctx, method, req)
		}
		s.received <- struct{}{}
		// The session's context ends when the shared server closes its
		// connection, so no response can be written for the held search.
		<-ctx.Done()
		return nil, ctx.Err()
	}
}

func isHeldSearch(method string, req mcp.Request) bool {
	call, ok := req.(*mcp.CallToolRequest)
	if method != "tools/call" || !ok || call.Params == nil || call.Params.Name != "search" {
		return false
	}
	var args struct {
		Query string `json:"query"`
	}
	return json.Unmarshal(call.Params.Arguments, &args) == nil && args.Query == HeldSearchQuery
}

func (h *SharedServerHarness) requireNoSpawn(caller string) {
	h.t.Helper()
	if h.Spawns() != 0 {
		h.t.Fatalf("%s must be called before the first spawn", caller)
	}
}

// BuildGate holds the builds of a harness after the ones it lets through.
type BuildGate struct {
	h        *SharedServerHarness
	held     chan struct{}
	released chan struct{}
	release  sync.Once
}

// GateBuildsAfter holds every build of h after the first passed builds until
// Release, so a session connecting to such a shared server waits for its
// ready line. Release runs when the test ends. It must be called before the
// first spawn.
func (h *SharedServerHarness) GateBuildsAfter(passed int) *BuildGate {
	h.t.Helper()
	h.requireNoSpawn("GateBuildsAfter")
	g := &BuildGate{h: h, held: make(chan struct{}, heldSearchBacklog), released: make(chan struct{})}
	var builds atomic.Int32
	h.buildGate = func(ctx context.Context) error {
		if builds.Add(1) <= int32(passed) {
			return nil
		}
		g.held <- struct{}{}
		select {
		case <-g.released:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	h.t.Cleanup(g.Release)
	return g
}

// AwaitHeld returns once one more build than earlier calls returned for is
// held, failing the test if none is within SharedServerDeadline.
func (g *BuildGate) AwaitHeld() {
	g.h.t.Helper()
	select {
	case <-g.held:
	case <-time.After(SharedServerDeadline):
		g.h.t.Fatalf("no build was held within %s", SharedServerDeadline)
	}
}

// Release lets every held and later build proceed.
func (g *BuildGate) Release() {
	g.release.Do(func() { close(g.released) })
}
