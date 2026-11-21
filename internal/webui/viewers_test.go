package webui_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/webui"
)

const heartbeatPath = "/api/v1/viewers/heartbeat"

type heartbeatRecorder struct {
	mu  sync.Mutex
	ids []string
}

func (r *heartbeatRecorder) record(viewerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, viewerID)
}

func (r *heartbeatRecorder) heard() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ids...)
}

func newViewerHandler(viewers *heartbeatRecorder) http.Handler {
	mux := http.NewServeMux()
	var opts []webui.MountOption
	if viewers != nil {
		opts = append(opts, webui.WithViewerHeartbeat(viewers.record))
	}
	webui.Mount(slog.New(slog.DiscardHandler), mux, &fakeBackend{}, newFakeReader(),
		config.UISettings{Enabled: true, MaxResults: testMaxResults}, opts...)
	return mux
}

func postHeartbeat(h http.Handler, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, heartbeatPath, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestViewerHeartbeat_ReportsTheViewerAndAnswersNoContent(t *testing.T) {
	viewers := &heartbeatRecorder{}
	h := newViewerHandler(viewers)

	rec := postHeartbeat(h, "application/json", `{"viewer_id":"3f2b-a_9"}`)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Equal(t, []string{"3f2b-a_9"}, viewers.heard())
}

func TestViewerHeartbeat_AcceptsJSONWithACharset(t *testing.T) {
	viewers := &heartbeatRecorder{}

	rec := postHeartbeat(newViewerHandler(viewers), "application/json; charset=utf-8", `{"viewer_id":"a"}`)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, []string{"a"}, viewers.heard())
}

func TestViewerHeartbeat_IsAcceptedWithoutEffectWhenNothingTracksViewers(t *testing.T) {
	rec := postHeartbeat(newViewerHandler(nil), "application/json", `{"viewer_id":"a"}`)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestViewerHeartbeat_RejectsAnInvalidViewerID(t *testing.T) {
	cases := map[string]string{
		"missing":       `{}`,
		"empty":         `{"viewer_id":""}`,
		"not a string":  `{"viewer_id":7}`,
		"bad character": `{"viewer_id":"a b"}`,
		"too long":      `{"viewer_id":"` + strings.Repeat("a", 65) + `"}`,
		"malformed":     `{"viewer_id":`,
		"empty body":    ``,
		"trailing data": `{"viewer_id":"a"} {"viewer_id":"b"}`,
		"trailing junk": `{"viewer_id":"a"}garbage`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			viewers := &heartbeatRecorder{}

			rec := postHeartbeat(newViewerHandler(viewers), "application/json", body)

			assertAPIError(t, rec, http.StatusBadRequest, "viewer_id must be 1 to 64 letters, digits, '-' or '_'")
			assert.Empty(t, viewers.heard())
		})
	}
}

func TestViewerHeartbeat_AcceptsTheLongestViewerID(t *testing.T) {
	viewers := &heartbeatRecorder{}
	id := strings.Repeat("a", 64)

	rec := postHeartbeat(newViewerHandler(viewers), "application/json", `{"viewer_id":"`+id+`"}`)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, []string{id}, viewers.heard())
}

func TestViewerHeartbeat_RequiresAJSONContentType(t *testing.T) {
	for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded"} {
		t.Run(contentType, func(t *testing.T) {
			viewers := &heartbeatRecorder{}

			rec := postHeartbeat(newViewerHandler(viewers), contentType, `{"viewer_id":"a"}`)

			assertAPIError(t, rec, http.StatusUnsupportedMediaType, "content type must be application/json")
			assert.Empty(t, viewers.heard())
		})
	}
}

func TestViewerHeartbeat_OnlyPostIsAllowed(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			viewers := &heartbeatRecorder{}

			rec := serve(newViewerHandler(viewers), method, heartbeatPath)

			assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
			assert.Equal(t, http.MethodPost, rec.Header().Get("Allow"))
			assertAPIHeaders(t, rec)
			assert.Empty(t, viewers.heard())
		})
	}
}

func TestAPI_JSONEndpointsDoNotAllowPost(t *testing.T) {
	rec := serve(newViewerHandler(nil), http.MethodPost, "/api/v1/config")

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, http.MethodGet, rec.Header().Get("Allow"))
}

func TestViewerHeartbeat_RejectsABodyOverOneKiB(t *testing.T) {
	cases := map[string]string{
		"padded object":    `{"viewer_id":"a"}` + strings.Repeat(" ", 1024),
		"oversized suffix": `{"viewer_id":"a"}` + strings.Repeat("x", 4096),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			viewers := &heartbeatRecorder{}

			rec := postHeartbeat(newViewerHandler(viewers), "application/json", body)

			assertAPIError(t, rec, http.StatusRequestEntityTooLarge, "request body too large")
			assert.Empty(t, viewers.heard())
		})
	}
}

func TestViewerHeartbeat_AcceptsABodyOfExactlyOneKiB(t *testing.T) {
	viewers := &heartbeatRecorder{}
	object := `{"viewer_id":"a"}`
	body := object + strings.Repeat(" ", 1024-len(object))

	rec := postHeartbeat(newViewerHandler(viewers), "application/json", body)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, []string{"a"}, viewers.heard())
}
