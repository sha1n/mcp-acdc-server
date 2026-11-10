// Package webui serves the embedded web search UI and its JSON API.
package webui

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/yuin/goldmark"
)

const (
	defaultLabelsLimit = 10
	markOpen           = "<mark>"
	markClose          = "</mark>"
)

//go:embed static
var staticFiles embed.FS

// Backend is the search capability the web UI depends on.
type Backend interface {
	SearchLabeled(query string, labels []string, limit int) ([]search.SearchResult, error)
	Labels(prefix string, limit int) ([]search.LabelCount, error)
}

type handler struct {
	backend  Backend
	settings config.UISettings
	markdown goldmark.Markdown
}

// NewHandler returns a handler serving the UI under /ui/ and the JSON API
// under /api/v1/. Callers mount it at those paths unchanged.
func NewHandler(backend Backend, settings config.UISettings) http.Handler {
	h := &handler{backend: backend, settings: settings, markdown: goldmark.New()}

	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err) // The embedded directory is fixed at compile time.
	}

	mux := http.NewServeMux()
	mux.Handle("/ui/", http.StripPrefix("/ui/", http.FileServer(http.FS(static))))
	mux.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/api/v1/", h.serveAPI)
	return mux
}

func (h *handler) serveAPI(w http.ResponseWriter, r *http.Request) {
	routes := map[string]http.HandlerFunc{
		"/api/v1/config": h.handleConfig,
		"/api/v1/labels": h.handleLabels,
		"/api/v1/search": h.handleSearch,
	}
	route, ok := routes[r.URL.Path]
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	route(w, r)
}

type configResponse struct {
	DefaultLimit int `json:"default_limit"`
	MaxLimit     int `json:"max_limit"`
}

func (h *handler) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, configResponse{DefaultLimit: h.settings.MaxResults, MaxLimit: config.MaxUIResultLimit})
}

type labelJSON struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type labelsResponse struct {
	Labels []labelJSON `json:"labels"`
}

func (h *handler) handleLabels(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, err := parseLimit(query.Get("limit"), defaultLabelsLimit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	labels, err := h.backend.Labels(strings.TrimSpace(query.Get("q")), limit)
	if err != nil {
		slog.Error("web UI label listing failed", "error", err)
		writeError(w, http.StatusInternalServerError, "label listing failed")
		return
	}

	resp := labelsResponse{Labels: make([]labelJSON, 0, len(labels))}
	for _, label := range labels {
		resp.Labels = append(resp.Labels, labelJSON{Name: label.Name, Count: label.Count})
	}
	writeJSON(w, http.StatusOK, resp)
}

type searchResultJSON struct {
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
	Snippet     string   `json:"snippet"`
	Content     string   `json:"content"`
	ContentHTML string   `json:"content_html"`
}

type searchResponse struct {
	Query   string             `json:"query"`
	Labels  []string           `json:"labels"`
	Limit   int                `json:"limit"`
	Total   int                `json:"total"`
	Results []searchResultJSON `json:"results"`
}

func (h *handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, err := parseLimit(query.Get("limit"), h.settings.MaxResults)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	resp := searchResponse{
		Query:   strings.TrimSpace(query.Get("q")),
		Labels:  nonEmpty(query["label"]),
		Limit:   limit,
		Results: []searchResultJSON{},
	}
	if resp.Query == "" && len(resp.Labels) == 0 {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	results, err := h.backend.SearchLabeled(resp.Query, resp.Labels, limit)
	if err != nil {
		slog.Error("web UI search failed", "error", err)
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}

	for _, result := range results {
		resp.Results = append(resp.Results, h.toResultJSON(result))
	}
	resp.Total = len(resp.Results)
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) toResultJSON(result search.SearchResult) searchResultJSON {
	return searchResultJSON{
		ChunkID:     result.ChunkID,
		SourceURI:   result.SourceURI,
		ChunkURI:    result.ChunkURI,
		Title:       result.SourceTitle,
		Path:        result.SourcePath,
		HeadingPath: orEmpty(result.HeadingPath),
		Labels:      orEmpty(result.Labels),
		StartLine:   result.StartLine,
		EndLine:     result.EndLine,
		Score:       result.Score,
		Snippet:     sanitizeSnippet(result.Snippet),
		Content:     result.Content,
		ContentHTML: h.renderMarkdown(result.Content),
	}
}

// renderMarkdown relies on goldmark's default renderer, which omits raw HTML
// rather than passing it through.
func (h *handler) renderMarkdown(content string) string {
	var buf bytes.Buffer
	if err := h.markdown.Convert([]byte(content), &buf); err != nil {
		return html.EscapeString(content)
	}
	return buf.String()
}

// sanitizeSnippet re-escapes a snippet so that <mark> is its only markup.
// Highlighted snippets arrive HTML-escaped by bleve while fallback snippets
// are raw chunk text, so the input is unescaped first to normalise both forms
// and avoid double escaping.
func sanitizeSnippet(snippet string) string {
	text := html.UnescapeString(snippet)
	var out strings.Builder
	open := false
	for text != "" {
		tag := markClose
		if !open {
			tag = markOpen
		}
		idx := strings.Index(text, tag)
		if idx < 0 {
			out.WriteString(html.EscapeString(stripMarks(text)))
			break
		}
		out.WriteString(html.EscapeString(stripMarks(text[:idx])))
		out.WriteString(tag)
		open = !open
		text = text[idx+len(tag):]
	}
	if open {
		out.WriteString(markClose)
	}
	return out.String()
}

func stripMarks(text string) string {
	return strings.NewReplacer(markOpen, "", markClose, "").Replace(text)
}

func parseLimit(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > config.MaxUIResultLimit {
		return 0, fmt.Errorf("limit must be an integer between 1 and %d, got: %q", config.MaxUIResultLimit, raw)
	}
	return limit, nil
}

func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("web UI response encoding failed", "error", err)
	}
}
