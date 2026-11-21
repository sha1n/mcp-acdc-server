package webui

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
)

// viewerHeartbeatPath is the API path, under /api/v1/, that a viewer posts
// its heartbeat to.
const viewerHeartbeatPath = "viewers/heartbeat"

// maxHeartbeatBody bounds the heartbeat request body.
const maxHeartbeatBody = 1 << 10

var (
	validViewerID       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	errInvalidViewerID  = apiError{http.StatusBadRequest, "viewer_id must be 1 to 64 letters, digits, '-' or '_'"}
	errUnsupportedMedia = apiError{http.StatusUnsupportedMediaType, "content type must be application/json"}
	errBodyTooLarge     = apiError{http.StatusRequestEntityTooLarge, "request body too large"}
)

// ViewerHeartbeat is told the id of every viewer that sends a heartbeat.
type ViewerHeartbeat func(viewerID string)

// MountOption configures optional web UI capabilities.
type MountOption func(*apiHandler)

// WithViewerHeartbeat reports every viewer heartbeat to heartbeat. Without
// it, heartbeats are accepted and have no effect.
func WithViewerHeartbeat(heartbeat ViewerHeartbeat) MountOption {
	return func(h *apiHandler) {
		h.heartbeat = heartbeat
	}
}

type heartbeatRequest struct {
	ViewerID string `json:"viewer_id"`
}

// viewerHeartbeat accepts a POST with a JSON body {"viewer_id": "<id>"} and
// answers 204. Requiring a JSON content type keeps a cross-origin page from
// sending it without a CORS preflight, which the API never grants.
func (h *apiHandler) viewerHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		h.writeAPIError(w, errMethodNotAllowed)
		return
	}
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		h.writeAPIError(w, errUnsupportedMedia)
		return
	}
	viewerID, err := readViewerID(r.Body)
	if err != nil {
		h.writeAPIError(w, h.toAPIError(r, err))
		return
	}
	if h.heartbeat != nil {
		h.heartbeat(viewerID)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// readViewerID reads a body of at most maxHeartbeatBody bytes that holds
// exactly one JSON object with a valid viewer_id, and nothing after it.
func readViewerID(body io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxHeartbeatBody+1))
	if err != nil {
		return "", fmt.Errorf("read heartbeat body: %w", err)
	}
	if len(data) > maxHeartbeatBody {
		return "", errBodyTooLarge
	}
	var request heartbeatRequest
	if err := json.Unmarshal(data, &request); err != nil || !validViewerID.MatchString(request.ViewerID) {
		return "", errInvalidViewerID
	}
	return request.ViewerID, nil
}
