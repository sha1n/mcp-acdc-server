//go:build unix

package testkit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// ReceivedMessage is one message a shared server's receiving middleware was
// invoked for.
type ReceivedMessage struct {
	Method string
	// Detail tells messages of one method apart: the query of a search, the
	// request id a cancellation names, and empty otherwise.
	Detail string
}

// ReceivedMessages records the messages every shared server of a harness
// hands to its receiving middleware, in invocation order. The SDK invokes it
// for a notification before it dequeues the next message, but for a call only
// after letting later messages proceed, so calls may be recorded out of read
// order.
type ReceivedMessages struct {
	mu       sync.Mutex
	received []ReceivedMessage
}

// RecordReceived makes every shared server h builds record the messages it
// receives. Searches a HoldSearches installed earlier holds are not recorded.
// It must be called before the first spawn.
func (h *SharedServerHarness) RecordReceived() *ReceivedMessages {
	h.t.Helper()
	h.requireNoSpawn("RecordReceived")
	r := &ReceivedMessages{}
	h.receiving = append(h.receiving, r.middleware)
	return r
}

// Received returns the messages recorded so far.
func (r *ReceivedMessages) Received() []ReceivedMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.received)
}

func (r *ReceivedMessages) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		r.mu.Lock()
		r.received = append(r.received, ReceivedMessage{Method: method, Detail: receivedDetail(req)})
		r.mu.Unlock()
		return next(ctx, method, req)
	}
}

func receivedDetail(req mcp.Request) string {
	switch params := req.GetParams().(type) {
	case *mcp.CancelledParams:
		return fmt.Sprint(params.RequestID)
	case *mcp.CallToolParamsRaw:
		var args struct {
			Query string `json:"query"`
		}
		if json.Unmarshal(params.Arguments, &args) == nil {
			return args.Query
		}
	}
	return ""
}

// WriteUnterminated writes text to the launch's stdin without a newline and
// returns once the launch has read it. A read of the stdin pipe never spans
// two writes, so by then the launch has taken every earlier line whole.
func (l *LaunchUnderTest) WriteUnterminated(text string) {
	l.t.Helper()
	_, err := io.WriteString(l.stdin, text)
	require.NoError(l.t, err)
}
