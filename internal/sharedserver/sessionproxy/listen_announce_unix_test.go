//go:build unix

package sessionproxy_test

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/sessionproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lineGatedOutput passes host output through, except that the write of one
// chosen line reports that it started and then blocks until released.
type lineGatedOutput struct {
	next    io.Writer
	gated   string
	entered chan struct{}
	gate    chan struct{}
	once    sync.Once
}

// gateHostOutputAt runs inside the fixture's configure, before the fixture
// registers its runner join: the caller registers release after the fixture
// returns, so the gate opens before that join.
func gateHostOutputAt(cfg *sessionproxy.Config, line string) *lineGatedOutput {
	o := &lineGatedOutput{next: cfg.Stdout, gated: line, entered: make(chan struct{}, 1), gate: make(chan struct{})}
	cfg.Stdout = o
	return o
}

func (o *lineGatedOutput) Write(p []byte) (int, error) {
	if string(p) == o.gated {
		o.entered <- struct{}{}
		<-o.gate
		return len(p), nil
	}
	return o.next.Write(p)
}

func (o *lineGatedOutput) release() { o.once.Do(func() { close(o.gate) }) }

func (o *lineGatedOutput) awaitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-o.entered:
	case <-time.After(proxyTestDeadline):
		t.Fatal("the session proxy never started the gated write to the host")
	}
}

func TestRun_SessionEndWhileAnAnnouncementWriteBlocksEndsTheSession(t *testing.T) {
	tests := []struct {
		name string
		end  func(o *outageFixture)
	}{
		{name: "stdin EOF", end: func(o *outageFixture) { require.NoError(o.t, o.host.stdin.Close()) }},
		{name: "cancellation", end: func(o *outageFixture) { o.host.cancel() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output *lineGatedOutput
			o := startOutage(t, func(cfg *sessionproxy.Config) {
				output = gateHostOutputAt(cfg, announcement(resourcesKind, "7"))
			})
			t.Cleanup(output.release)
			openStream(t, o.host, o.old, listen("7", resourcesKind, promptsKind), acknowledged("7", resourcesKind, promptsKind))
			o.crash()
			recovered := replacementListener(t, o)
			o.hold(request("1"))
			o.release(proxyTestIdentity, nil)
			conn := recovered.next()
			conn.sendReadyAs("second")
			require.Equal(t, listen("7", resourcesKind, promptsKind), conn.readLine())
			conn.write(acknowledged("7", resourcesKind, promptsKind))
			output.awaitEntered(t)

			tt.end(o)

			assert.NoError(t, o.host.result(), "a session end, not a failed recovery")
			conn.requireClosedByProxy() // the held request was never sent
			assert.Equal(t, int32(1), recovered.count.Load(), "no rediscovery")
		})
	}
}
