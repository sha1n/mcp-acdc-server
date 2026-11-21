package sharedserver

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingUIHandler serves the max results it was built with, so a test can
// see which request's value the running UI uses.
type recordingUIHandler struct {
	mu    sync.Mutex
	calls []int
}

func (r *recordingUIHandler) build(maxResults int) (http.Handler, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, maxResults)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(w, req.URL.Path+" "+strconv.Itoa(maxResults))
	}), nil
}

func (r *recordingUIHandler) builtWith() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.calls...)
}

func newTestWebUI(t *testing.T, logs io.Writer, handler UIHandlerFunc) *webUI {
	t.Helper()
	ui := newWebUI(slog.New(slog.NewTextHandler(logs, nil)))
	ui.setHandler(handler)
	t.Cleanup(ui.close)
	return ui
}

func getBody(t *testing.T, target string) string {
	t.Helper()
	resp, err := http.Get(target) //nolint:gosec // test URL from the UI under test
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

func TestWebUI_StartsOnLoopbackAndServesTheHandler(t *testing.T) {
	handler := &recordingUIHandler{}
	ui := newTestWebUI(t, io.Discard, handler.build)

	uiURL, err := ui.ensure(7)
	require.NoError(t, err)

	parsed, err := url.Parse(uiURL)
	require.NoError(t, err)
	assert.Equal(t, "http", parsed.Scheme)
	assert.Equal(t, "127.0.0.1", parsed.Hostname())
	assert.NotEqual(t, "0", parsed.Port())
	assert.Equal(t, "/ui/", parsed.Path)
	assert.Equal(t, "/ui/ 7", getBody(t, uiURL))
}

func TestWebUI_LaterRequestsGetTheSameURLAndTheFirstMaxResults(t *testing.T) {
	handler := &recordingUIHandler{}
	ui := newTestWebUI(t, io.Discard, handler.build)

	first, err := ui.ensure(7)
	require.NoError(t, err)
	second, err := ui.ensure(99)
	require.NoError(t, err)

	assert.Equal(t, first, second)
	assert.Equal(t, []int{7}, handler.builtWith())
	assert.Equal(t, "/ui/ 7", getBody(t, second))
}

func TestWebUI_LogsOneStartEntryWithItsURL(t *testing.T) {
	logs := &syncLogBuffer{}
	handler := &recordingUIHandler{}
	ui := newTestWebUI(t, logs, handler.build)

	uiURL, err := ui.ensure(7)
	require.NoError(t, err)
	_, err = ui.ensure(7)
	require.NoError(t, err)

	assert.Equal(t, 1, strings.Count(logs.String(), "Web UI started"))
	assert.Contains(t, logs.String(), "url="+uiURL)
}

func TestWebUI_BindFailureIsReturnedAndTheNextRequestTriesAgain(t *testing.T) {
	handler := &recordingUIHandler{}
	ui := newTestWebUI(t, io.Discard, handler.build)
	failFirstListen(ui)

	_, err := ui.ensure(7)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "address in use")

	uiURL, err := ui.ensure(9)
	require.NoError(t, err)
	assert.Equal(t, "/ui/ 9", getBody(t, uiURL), "the request that started the UI decides its max results")
}

func TestWebUI_HandlerFailureIsReturned(t *testing.T) {
	ui := newTestWebUI(t, io.Discard, func(int) (http.Handler, error) { return nil, errors.New("no components") })

	_, err := ui.ensure(7)
	assert.ErrorContains(t, err, "no components")
}

func TestWebUI_WithoutAHandlerIsUnavailable(t *testing.T) {
	ui := newTestWebUI(t, io.Discard, nil)

	_, err := ui.ensure(7)
	assert.Error(t, err)
}

func TestWebUI_CloseStopsServingAndRefusesLaterRequests(t *testing.T) {
	handler := &recordingUIHandler{}
	ui := newTestWebUI(t, io.Discard, handler.build)
	uiURL, err := ui.ensure(7)
	require.NoError(t, err)

	ui.close()

	_, err = http.Get(uiURL) //nolint:gosec // test URL from the UI under test
	assert.Error(t, err)
	_, err = ui.ensure(7)
	assert.Error(t, err)
}

// failFirstListen makes the first bind of ui fail, as when the port cannot
// be bound, and every later bind succeed.
func failFirstListen(ui *webUI) {
	listen := ui.listen
	var failed atomic.Bool
	ui.listen = func() (net.Listener, error) {
		if failed.CompareAndSwap(false, true) {
			return nil, errors.New("address in use")
		}
		return listen()
	}
}

func TestWebUI_BindsWithTheSuppliedListenAndKeepsLoopbackWithoutOne(t *testing.T) {
	handler := &recordingUIHandler{}
	ui := newTestWebUI(t, io.Discard, handler.build)
	ui.setListen(nil)
	ui.setListen(func() (net.Listener, error) { return nil, errors.New("supplied listen failed") })

	_, err := ui.ensure(7)

	assert.ErrorContains(t, err, "supplied listen failed")
}

// brokenListener is bound, but its Accept fails terminally, as a UI listener
// that stops serving does.
type brokenListener struct {
	net.Listener
}

func (l brokenListener) Accept() (net.Conn, error) {
	return nil, errors.New("listener broken")
}

func TestWebUI_ServeFailureIsReturnedAndTheNextRequestStartsAgain(t *testing.T) {
	handler := &recordingUIHandler{}
	ui := newTestWebUI(t, io.Discard, handler.build)
	listen := ui.listen
	var listens atomic.Int32
	ui.listen = func() (net.Listener, error) {
		listener, err := listen()
		if listens.Add(1) == 1 && err == nil {
			return brokenListener{Listener: listener}, nil
		}
		return listener, err
	}
	dead, err := ui.ensure(7)
	require.NoError(t, err)

	var serveErr error
	require.Eventually(t, func() bool {
		_, serveErr = ui.ensure(7)
		return serveErr != nil
	}, serveTestDeadline, 10*time.Millisecond, "a UI that stopped serving must not stay cached as ready")
	assert.ErrorContains(t, serveErr, "listener broken")

	uiURL, err := ui.ensure(9)
	require.NoError(t, err)
	assert.NotEqual(t, dead, uiURL)
	assert.Equal(t, "/ui/ 9", getBody(t, uiURL), "the request that started the UI again decides its max results")
}
