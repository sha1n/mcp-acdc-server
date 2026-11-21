//go:build unix

package sharedserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	viewerTestWindow   = time.Second
	viewerTestLinger   = time.Second // the linger testkit.SharedLaunchArgs sets
	viewerTestInterval = 100 * time.Millisecond
)

// postViewerHeartbeat sends one heartbeat of viewerID to the Web UI at uiURL.
func postViewerHeartbeat(t *testing.T, uiURL, viewerID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testkit.SharedServerDeadline)
	defer cancel()
	target := strings.TrimSuffix(uiURL, "/ui/") + "/api/v1/viewers/heartbeat"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(`{"viewer_id":"`+viewerID+`"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

// viewerHeartbeats sends heartbeats of one viewer until stop, which returns
// when the last heartbeat started being sent.
type viewerHeartbeats struct {
	done     chan struct{}
	stopOnce sync.Once
	finished sync.WaitGroup
	mu       sync.Mutex
	last     time.Time
	err      error
}

// startViewerHeartbeats stops its heartbeats when the test ends, so a test
// that fails before stop leaks no goroutine.
func startViewerHeartbeats(t *testing.T, uiURL, viewerID string) *viewerHeartbeats {
	t.Helper()
	v := &viewerHeartbeats{done: make(chan struct{})}
	target := strings.TrimSuffix(uiURL, "/ui/") + "/api/v1/viewers/heartbeat"
	v.finished.Add(1)
	t.Cleanup(func() { _, _ = v.stop() })
	go func() {
		defer v.finished.Done()
		ticker := time.NewTicker(viewerTestInterval)
		defer ticker.Stop()
		for {
			select {
			case <-v.done:
				return
			case <-ticker.C:
				v.beat(target, viewerID)
			}
		}
	}()
	return v
}

func (v *viewerHeartbeats) beat(target, viewerID string) {
	sent := time.Now()
	err := sendViewerHeartbeat(target, viewerID)
	v.mu.Lock()
	defer v.mu.Unlock()
	v.last = sent
	if err != nil && v.err == nil {
		v.err = err
	}
}

func sendViewerHeartbeat(target, viewerID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), testkit.SharedServerDeadline)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(`{"viewer_id":"`+viewerID+`"}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("heartbeat answered %d", resp.StatusCode)
	}
	return nil
}

// stop is idempotent.
func (v *viewerHeartbeats) stop() (lastSent time.Time, err error) {
	v.stopOnce.Do(func() { close(v.done) })
	v.finished.Wait()
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.last, v.err
}

// awaitBrowse returns the result of a browse run in the background, failing
// the test if it does not end within testkit.SharedServerDeadline.
func awaitBrowse(t *testing.T, browsed <-chan testkit.BrowseResult) testkit.BrowseResult {
	t.Helper()
	select {
	case r := <-browsed:
		return r
	case <-time.After(testkit.SharedServerDeadline):
		t.Fatalf("browse did not end within %s", testkit.SharedServerDeadline)
		return testkit.BrowseResult{}
	}
}

// requireExitAfter waits for the one shared server to exit and requires
// that it did not exit before earliest. The exits are read before the clock,
// so an exit seen before earliest is early.
func requireExitAfter(t *testing.T, h *testkit.SharedServerHarness, earliest time.Time, msg string) {
	t.Helper()
	var early atomic.Bool
	h.Eventually(func() bool {
		exits := h.BuiltExits()
		if len(exits) > 0 && time.Now().Before(earliest) {
			early.Store(true)
		}
		return len(exits) == 1
	}, msg)
	assert.False(t, early.Load(), "the shared server exited early: %s", msg)
}

func TestBrowse_AViewerKeepsTheSharedServerAlivePastItsLinger(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	h.ViewerWindow = viewerTestWindow
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t))
	l := h.Launch(args...)
	session := connectAndListTools(t, l)
	uiURL := requireBrowseURL(t, h.Browse(append(args, "--no-open")...))
	postViewerHeartbeat(t, uiURL, "viewer-1")
	heartbeats := startViewerHeartbeats(t, uiURL, "viewer-1")

	require.NoError(t, session.Close())
	require.NoError(t, l.Wait(testkit.SharedServerDeadline))
	time.Sleep(3 * viewerTestLinger)

	assert.Empty(t, h.BuiltExits(), "a live viewer holds the shared server past its linger")
	requireServesUI(t, uiURL)

	lastSent, err := heartbeats.stop()
	require.NoError(t, err)
	requireExitAfter(t, h, lastSent.Add(viewerTestWindow+viewerTestLinger),
		"the shared server must stop after the viewer window and the linger")
	assert.Equal(t, 1, h.Builds())
}

// With no session ever attached, a cold build that outlasts the linger must
// not end the shared server as soon as browse is answered: the page browse
// opened gets a whole linger from then to send its first heartbeat.
func TestBrowse_AColdStartOutlastingTheLingerLeavesThePageAWholeLinger(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	h.ViewerWindow = viewerTestWindow
	gate := h.GateBuildsAfter(0)
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t))
	browsed := make(chan testkit.BrowseResult, 1)
	go func() { browsed <- h.Browse(append(args, "--no-open")...) }()
	gate.AwaitHeld()
	time.Sleep(2 * viewerTestLinger)

	released := time.Now()
	gate.Release()
	requireBrowseURL(t, awaitBrowse(t, browsed))

	requireExitAfter(t, h, released.Add(viewerTestLinger),
		"the shared server must stop a linger after browse was answered")
	assert.Equal(t, 1, h.Builds())
}

func TestBrowse_ThePageOfAColdStartOutlastingTheLingerKeepsWorking(t *testing.T) {
	h := testkit.NewSharedServerHarness(t)
	h.ViewerWindow = viewerTestWindow
	gate := h.GateBuildsAfter(0)
	args := testkit.SharedLaunchArgs(newSessionProxyContentDir(t))
	browsed := make(chan testkit.BrowseResult, 1)
	go func() { browsed <- h.Browse(append(args, "--no-open")...) }()
	gate.AwaitHeld()
	time.Sleep(2 * viewerTestLinger)
	gate.Release()
	uiURL := requireBrowseURL(t, awaitBrowse(t, browsed))

	postViewerHeartbeat(t, uiURL, "viewer-1")
	heartbeats := startViewerHeartbeats(t, uiURL, "viewer-1")
	time.Sleep(3 * viewerTestLinger)

	assert.Empty(t, h.BuiltExits(), "the page browse opened holds the shared server")
	requireServesUI(t, uiURL)
	_, err := heartbeats.stop()
	require.NoError(t, err)
}
