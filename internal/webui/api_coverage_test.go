package webui_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type failingWriter struct {
	header http.Header
	status int
}

func (w *failingWriter) Header() http.Header       { return w.header }
func (w *failingWriter) WriteHeader(status int)    { w.status = status }
func (w *failingWriter) Write([]byte) (int, error) { return 0, errors.New("client went away") }

type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

// Not parallel: the handler logs through slog.Default, which this test swaps.
func TestAPI_WriteFailureIsLoggedAsWarning(t *testing.T) {
	recorder := &recordingHandler{}
	previous := slog.Default()
	slog.SetDefault(slog.New(recorder))
	t.Cleanup(func() { slog.SetDefault(previous) })

	w := &failingWriter{header: http.Header{}}
	newHandler(t, &fakeBackend{}, newFakeReader()).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))

	require.Equal(t, http.StatusOK, w.status)
	require.Len(t, recorder.records, 1)
	record := recorder.records[0]
	require.Equal(t, slog.LevelWarn, record.Level)
	require.Equal(t, "Failed to write web UI API response", record.Message)
	var cause string
	record.Attrs(func(a slog.Attr) bool {
		if a.Key == "error" {
			cause = a.Value.String()
		}
		return true
	})
	require.Contains(t, cause, "client went away")
}
