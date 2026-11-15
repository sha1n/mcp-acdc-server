//go:build unix

package testkit

import (
	"context"
	"encoding/json"
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

func (h *SharedServerHarness) addCandidate(c *runningCandidate) {
	h.candidatesMu.Lock()
	defer h.candidatesMu.Unlock()
	h.candidates = append(h.candidates, c)
}

// StopSharedServers ends every shared server candidate spawned so far by
// cancelling its goroutine (spec 13.3) and returns once each has returned,
// so every session of those servers has lost its connection by then. Later
// spawns are not affected. The test fails if one has not returned within
// SharedServerDeadline.
func (h *SharedServerHarness) StopSharedServers() {
	h.t.Helper()
	h.candidatesMu.Lock()
	stopping := h.candidates
	h.candidates = nil
	h.candidatesMu.Unlock()

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
	if h.Spawns() != 0 {
		h.t.Fatal("HoldSearches must be called before the first spawn")
	}
	h.heldSearches = &HeldSearches{h: h, received: make(chan struct{}, heldSearchBacklog)}
	return h.heldSearches
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
