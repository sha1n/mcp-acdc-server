package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/sha1n/mcp-acdc-server/internal/webui/render"
)

const (
	apiPrefix         = "/api/v1/"
	defaultLabelLimit = 10
)

type apiError struct {
	status  int
	message string
}

var (
	errNotFound         = apiError{http.StatusNotFound, "not found"}
	errMethodNotAllowed = apiError{http.StatusMethodNotAllowed, "method not allowed"}
	errURIRequired      = apiError{http.StatusBadRequest, "uri is required"}
	errDocumentNotFound = apiError{http.StatusNotFound, "document not found"}
	errInternal         = apiError{http.StatusInternalServerError, "internal server error"}
)

type apiHandler struct {
	backend   SearchBackend
	documents DocumentReader
	settings  config.UISettings
}

type endpoint func(*apiHandler, *http.Request) (any, error)

var endpoints = map[string]endpoint{
	"config":   (*apiHandler).config,
	"labels":   (*apiHandler).labels,
	"search":   (*apiHandler).search,
	"document": (*apiHandler).document,
}

type configResponse struct {
	DefaultLimit int `json:"default_limit"`
	MaxLimit     int `json:"max_limit"`
}

type labelsResponse struct {
	Labels []labelCount `json:"labels"`
}

type labelCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type searchResponse struct {
	Query   string         `json:"query"`
	Labels  []string       `json:"labels"`
	Limit   int            `json:"limit"`
	Total   int            `json:"total"`
	Results []searchResult `json:"results"`
}

type searchResult struct {
	ChunkID     string   `json:"chunk_id"`
	SourceURI   string   `json:"source_uri"`
	ChunkURI    string   `json:"chunk_uri"`
	Title       string   `json:"title"`
	Path        string   `json:"path"`
	HeadingPath []string `json:"heading_path"`
	Labels      []string `json:"labels"`
	StartLine   int      `json:"start_line"`
	EndLine     int      `json:"end_line"`
	Score       float64  `json:"score"`
	DocumentURL string   `json:"document_url"`
	Snippet     string   `json:"snippet"`
	Content     string   `json:"content"`
	ContentHTML string   `json:"content_html"`
}

type documentResponse struct {
	URI         string   `json:"uri"`
	Title       string   `json:"title"`
	Path        string   `json:"path"`
	Labels      []string `json:"labels"`
	Content     string   `json:"content"`
	ContentHTML string   `json:"content_html"`
}

func (h *apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	handle, ok := endpoints[strings.TrimPrefix(r.URL.Path, apiPrefix)]
	if !ok {
		writeAPIError(w, errNotFound)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, errMethodNotAllowed)
		return
	}
	body, err := handle(h, r)
	if err != nil {
		writeAPIError(w, toAPIError(r, err))
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (h *apiHandler) config(*http.Request) (any, error) {
	return configResponse{DefaultLimit: h.settings.MaxResults, MaxLimit: config.MaxUIResultLimit}, nil
}

func (h *apiHandler) labels(r *http.Request) (any, error) {
	query := r.URL.Query()
	limit, err := parseLimit(query.Get("limit"), defaultLabelLimit)
	if err != nil {
		return nil, err
	}
	counts, err := h.backend.Labels(r.Context(), strings.TrimSpace(query.Get("q")), limit)
	if err != nil {
		return nil, fmt.Errorf("list labels: %w", err)
	}
	labels := make([]labelCount, 0, len(counts))
	for _, c := range counts {
		labels = append(labels, labelCount{Name: c.Name, Count: c.Count})
	}
	return labelsResponse{Labels: labels}, nil
}

func (h *apiHandler) search(r *http.Request) (any, error) {
	query := r.URL.Query()
	limit, err := parseLimit(query.Get("limit"), h.settings.MaxResults)
	if err != nil {
		return nil, err
	}
	response := searchResponse{
		Query:   strings.TrimSpace(query.Get("q")),
		Labels:  domain.Labels(query["label"], nil),
		Limit:   limit,
		Results: []searchResult{},
	}
	if response.Query == "" && len(response.Labels) == 0 {
		return response, nil
	}
	results, err := h.backend.LabelledSearch(r.Context(), response.Query, response.Labels, limit)
	if err != nil {
		return nil, fmt.Errorf("search %q: %w", response.Query, err)
	}
	if len(results) > 0 {
		if response.Results, err = toSearchResults(results, h.documents.Snapshot(r.Context())); err != nil {
			return nil, err
		}
	}
	response.Total = len(response.Results)
	return response, nil
}

func (h *apiHandler) document(r *http.Request) (any, error) {
	uri := strings.TrimSpace(r.URL.Query().Get("uri"))
	if uri == "" {
		return nil, errURIRequired
	}
	snapshot := h.documents.Snapshot(r.Context())
	doc, err := snapshot.Document(uri)
	if err != nil {
		return nil, fmt.Errorf("read document %q: %w", uri, err)
	}
	return documentResponse{
		URI:     doc.URI,
		Title:   doc.Title,
		Path:    doc.Path,
		Labels:  nonNil(doc.Labels),
		Content: doc.Content,
		ContentHTML: render.Markdown(doc.Content, render.Options{
			FromPath: doc.Path, HeadingIDs: true, HeadingSource: doc.Source, Links: snapshot,
		}),
	}, nil
}

func toSearchResults(results []search.LabelledResult, snapshot DocumentSnapshot) ([]searchResult, error) {
	sources := sourceReferences{snapshot: snapshot, byURI: map[string]render.References{}}
	mapped := make([]searchResult, 0, len(results))
	for _, r := range results {
		references, err := sources.collect(r.SourceURI)
		if err != nil {
			return nil, err
		}
		mapped = append(mapped, searchResult{
			ChunkID:     r.ChunkID,
			SourceURI:   r.SourceURI,
			ChunkURI:    r.ChunkURI,
			Title:       r.SourceTitle,
			Path:        r.SourcePath,
			HeadingPath: nonNil(r.HeadingPath),
			Labels:      nonNil(r.Labels),
			StartLine:   r.StartLine,
			EndLine:     r.EndLine,
			Score:       r.Score,
			DocumentURL: render.ResultDocumentURL(r.SourceURI, r.ChunkURI),
			Snippet:     render.Snippet(r.Snippet),
			Content:     r.Content,
			ContentHTML: render.Markdown(r.Content, render.Options{
				FromPath: r.SourcePath, SourceURI: r.SourceURI, HeadingIDs: false,
				References: references, Links: snapshot,
			}),
		})
	}
	return mapped, nil
}

// sourceReferences memoises collected reference definitions for one request,
// since many results commonly share a source document and collecting them
// parses the whole document.
type sourceReferences struct {
	snapshot DocumentSnapshot
	byURI    map[string]render.References
}

// A missing source document is tolerated because search results and the
// snapshot are separate reads of the catalog; the excerpt then renders with its
// own definitions only.
func (s sourceReferences) collect(uri string) (render.References, error) {
	if references, ok := s.byURI[uri]; ok {
		return references, nil
	}
	doc, err := s.snapshot.Document(uri)
	if err != nil && !errors.Is(err, ErrDocumentNotFound) {
		return render.References{}, fmt.Errorf("read source document %q: %w", uri, err)
	}
	references := render.CollectReferences(doc.Content)
	s.byURI[uri] = references
	return references, nil
}

func parseLimit(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > config.MaxUIResultLimit {
		return 0, apiError{
			status:  http.StatusBadRequest,
			message: fmt.Sprintf("limit must be an integer between 1 and %d", config.MaxUIResultLimit),
		}
	}
	return limit, nil
}

func toAPIError(r *http.Request, err error) apiError {
	var known apiError
	if errors.As(err, &known) {
		return known
	}
	if errors.Is(err, ErrDocumentNotFound) {
		return errDocumentNotFound
	}
	slog.Error("Web UI API request failed", "path", r.URL.Path, "error", err)
	return errInternal
}

func (e apiError) Error() string {
	return e.message
}

func writeAPIError(w http.ResponseWriter, e apiError) {
	writeJSON(w, e.status, map[string]string{"error": e.Error()})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	header := w.Header()
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Warn("Failed to write web UI API response", "error", err)
	}
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
