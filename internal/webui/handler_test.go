package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/stretchr/testify/require"
)

const jsonContentType = "application/json; charset=utf-8"

type searchCall struct {
	query  string
	labels []string
	limit  int
}

type labelsCall struct {
	prefix string
	limit  int
}

type fakeBackend struct {
	results     []search.SearchResult
	labels      []search.LabelCount
	err         error
	searchCalls []searchCall
	labelsCalls []labelsCall
}

func (f *fakeBackend) SearchLabeled(query string, labels []string, limit int) ([]search.SearchResult, error) {
	f.searchCalls = append(f.searchCalls, searchCall{query: query, labels: labels, limit: limit})
	return f.results, f.err
}

func (f *fakeBackend) Labels(prefix string, limit int) ([]search.LabelCount, error) {
	f.labelsCalls = append(f.labelsCalls, labelsCall{prefix: prefix, limit: limit})
	return f.labels, f.err
}

func testUISettings() config.UISettings {
	return config.UISettings{Enabled: true, MaxResults: 15}
}

func serve(t *testing.T, backend Backend, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	NewHandler(backend, testUISettings()).ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), v), rec.Body.String())
}

func requireJSONError(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	require.Equal(t, status, rec.Code, rec.Body.String())
	require.Equal(t, jsonContentType, rec.Header().Get("Content-Type"))
	var body map[string]any
	decode(t, rec, &body)
	require.Len(t, body, 1, rec.Body.String())
	msg, ok := body["error"].(string)
	require.True(t, ok, rec.Body.String())
	require.NotEmpty(t, msg)
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestHandler_Config(t *testing.T) {
	rec := serve(t, &fakeBackend{}, http.MethodGet, "/api/v1/config")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, jsonContentType, rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"default_limit":15,"max_limit":100}`, rec.Body.String())
}

func TestHandler_Labels_DefaultsAndShape(t *testing.T) {
	backend := &fakeBackend{labels: []search.LabelCount{{Name: "deployment", Count: 12}, {Name: "docker", Count: 3}}}

	rec := serve(t, backend, http.MethodGet, "/api/v1/labels")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, jsonContentType, rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"labels":[{"name":"deployment","count":12},{"name":"docker","count":3}]}`, rec.Body.String())
	require.Equal(t, []labelsCall{{prefix: "", limit: 10}}, backend.labelsCalls)
}

func TestHandler_Labels_PassesPrefixAndLimit(t *testing.T) {
	backend := &fakeBackend{}

	rec := serve(t, backend, http.MethodGet, "/api/v1/labels?q=dep&limit=100")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []labelsCall{{prefix: "dep", limit: 100}}, backend.labelsCalls)
}

func TestHandler_Labels_EmptyIsArrayNotNull(t *testing.T) {
	rec := serve(t, &fakeBackend{}, http.MethodGet, "/api/v1/labels?q=zzz")

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"labels":[]}`, rec.Body.String())
}

func TestHandler_Labels_InvalidLimit(t *testing.T) {
	for _, limit := range []string{"0", "-1", "101", "abc", "1.5"} {
		t.Run(limit, func(t *testing.T) {
			backend := &fakeBackend{}

			rec := serve(t, backend, http.MethodGet, "/api/v1/labels?limit="+url.QueryEscape(limit))

			requireJSONError(t, rec, http.StatusBadRequest)
			require.Empty(t, backend.labelsCalls)
		})
	}
}

func TestHandler_Labels_BackendFailure(t *testing.T) {
	rec := serve(t, &fakeBackend{err: errors.New("boom")}, http.MethodGet, "/api/v1/labels")

	requireJSONError(t, rec, http.StatusInternalServerError)
}

func TestHandler_Search_ShapeAndParams(t *testing.T) {
	backend := &fakeBackend{results: []search.SearchResult{{
		ChunkID: "guide#oauth", SourceID: "guide", SourceURI: "acdc://guide", ChunkURI: "acdc://guide#oauth",
		SourceTitle: "OAuth guide", SourcePath: "guides/oauth.md", HeadingPath: []string{"A", "B"},
		Labels: []string{"deployment"}, StartLine: 1, EndLine: 9, Score: 1.23,
		Snippet: "text with <mark>hit</mark>", Content: "raw markdown",
	}}}

	rec := serve(t, backend, http.MethodGet, "/api/v1/search?q=docker&label=deployment&label=Guides&limit=20")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, jsonContentType, rec.Header().Get("Content-Type"))
	require.Equal(t, []searchCall{{query: "docker", labels: []string{"deployment", "Guides"}, limit: 20}}, backend.searchCalls)

	var raw map[string]any
	decode(t, rec, &raw)
	require.Equal(t, []string{"labels", "limit", "query", "results", "total"}, keys(raw))
	require.Equal(t, "docker", raw["query"])
	require.Equal(t, []any{"deployment", "Guides"}, raw["labels"])
	require.EqualValues(t, 20, raw["limit"])
	require.EqualValues(t, 1, raw["total"])

	results := raw["results"].([]any)
	require.Len(t, results, 1)
	result := results[0].(map[string]any)
	require.Equal(t, []string{
		"chunk_id", "chunk_uri", "content", "content_html", "end_line", "heading_path", "labels",
		"path", "score", "snippet", "source_uri", "start_line", "title",
	}, keys(result))
	require.Equal(t, "guide#oauth", result["chunk_id"])
	require.Equal(t, "acdc://guide", result["source_uri"])
	require.Equal(t, "acdc://guide#oauth", result["chunk_uri"])
	require.Equal(t, "OAuth guide", result["title"])
	require.Equal(t, "guides/oauth.md", result["path"])
	require.Equal(t, []any{"A", "B"}, result["heading_path"])
	require.Equal(t, []any{"deployment"}, result["labels"])
	require.EqualValues(t, 1, result["start_line"])
	require.EqualValues(t, 9, result["end_line"])
	require.InDelta(t, 1.23, result["score"], 1e-9)
	require.Equal(t, "text with <mark>hit</mark>", result["snippet"])
	require.Equal(t, "raw markdown", result["content"])
	require.Equal(t, "<p>raw markdown</p>", strings.TrimSpace(result["content_html"].(string)))
}

func TestHandler_Search_DefaultLimitIsUIMaxResults(t *testing.T) {
	backend := &fakeBackend{}

	rec := serve(t, backend, http.MethodGet, "/api/v1/search?q=docker")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, backend.searchCalls, 1)
	require.Equal(t, 15, backend.searchCalls[0].limit)
	var raw map[string]any
	decode(t, rec, &raw)
	require.EqualValues(t, 15, raw["limit"])
}

func TestHandler_Search_MaxLimitIsHardCapNotUIMaxResults(t *testing.T) {
	backend := &fakeBackend{}

	rec := serve(t, backend, http.MethodGet, "/api/v1/search?q=docker&limit=100")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, backend.searchCalls, 1)
	require.Equal(t, 100, backend.searchCalls[0].limit)
}

func TestHandler_Search_ArraysAreNeverNull(t *testing.T) {
	backend := &fakeBackend{results: []search.SearchResult{{ChunkID: "c1", Content: "x"}}}

	rec := serve(t, backend, http.MethodGet, "/api/v1/search?q=docker")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var raw map[string]any
	decode(t, rec, &raw)
	require.Equal(t, []any{}, raw["labels"])
	result := raw["results"].([]any)[0].(map[string]any)
	require.Equal(t, []any{}, result["heading_path"])
	require.Equal(t, []any{}, result["labels"])
}

func TestHandler_Search_NoResultsIsEmptyArray(t *testing.T) {
	rec := serve(t, &fakeBackend{}, http.MethodGet, "/api/v1/search?q=nothing")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"query":"nothing","labels":[],"limit":15,"total":0,"results":[]}`, rec.Body.String())
}

func TestHandler_Search_EmptyQueryAndNoLabelsReturnsEmptyResults(t *testing.T) {
	backend := &fakeBackend{results: []search.SearchResult{{ChunkID: "should-not-appear"}}}

	rec := serve(t, backend, http.MethodGet, "/api/v1/search")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"query":"","labels":[],"limit":15,"total":0,"results":[]}`, rec.Body.String())
}

func TestHandler_Search_EmptyQueryWithLabelsQueriesBackend(t *testing.T) {
	backend := &fakeBackend{results: []search.SearchResult{{ChunkID: "c1"}, {ChunkID: "c2"}}}

	rec := serve(t, backend, http.MethodGet, "/api/v1/search?label=deployment")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []searchCall{{query: "", labels: []string{"deployment"}, limit: 15}}, backend.searchCalls)
	var raw map[string]any
	decode(t, rec, &raw)
	require.EqualValues(t, 2, raw["total"])
	require.Len(t, raw["results"], 2)
}

func TestHandler_Search_InvalidLimit(t *testing.T) {
	for _, limit := range []string{"0", "-1", "101", "abc"} {
		t.Run(limit, func(t *testing.T) {
			backend := &fakeBackend{}

			rec := serve(t, backend, http.MethodGet, "/api/v1/search?q=docker&limit="+url.QueryEscape(limit))

			requireJSONError(t, rec, http.StatusBadRequest)
			require.Empty(t, backend.searchCalls)
		})
	}
}

func TestHandler_Search_BackendFailure(t *testing.T) {
	rec := serve(t, &fakeBackend{err: errors.New("boom")}, http.MethodGet, "/api/v1/search?q=docker")

	requireJSONError(t, rec, http.StatusInternalServerError)
}

func TestHandler_Search_SnippetOnlyAllowsMarkTags(t *testing.T) {
	backend := &fakeBackend{results: []search.SearchResult{{
		ChunkID: "c1",
		Snippet: `<script>alert(1)</script> a & b <img src=x onerror=y> <mark>hit</mark>`,
	}}}

	rec := serve(t, backend, http.MethodGet, "/api/v1/search?q=hit")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var raw map[string]any
	decode(t, rec, &raw)
	snippet := raw["results"].([]any)[0].(map[string]any)["snippet"].(string)
	require.Contains(t, snippet, "<mark>hit</mark>")
	require.Contains(t, snippet, "&lt;script&gt;")
	require.Contains(t, snippet, "&amp;")
	require.NotContains(t, snippet, "<script")
	require.NotContains(t, snippet, "<img")
	withoutMarks := strings.NewReplacer("<mark>", "", "</mark>", "").Replace(snippet)
	require.NotContains(t, withoutMarks, "<")
	require.NotContains(t, withoutMarks, ">")
}

func TestHandler_Search_ContentHTMLIsRenderedMarkdownWithoutRawHTML(t *testing.T) {
	content := "# Title\n\nSome **bold** text.\n\n<script>alert(1)</script>\n\n<div onclick=\"x\">raw</div>\n"
	backend := &fakeBackend{results: []search.SearchResult{{ChunkID: "c1", Content: content}}}

	rec := serve(t, backend, http.MethodGet, "/api/v1/search?q=bold")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var raw map[string]any
	decode(t, rec, &raw)
	result := raw["results"].([]any)[0].(map[string]any)
	require.Equal(t, content, result["content"])
	html := result["content_html"].(string)
	require.Contains(t, html, "<h1>Title</h1>")
	require.Contains(t, html, "<strong>bold</strong>")
	require.NotContains(t, html, "<script")
	require.NotContains(t, html, "onclick")
}

func TestHandler_APIRejectsNonGET(t *testing.T) {
	for _, path := range []string{"/api/v1/config", "/api/v1/labels", "/api/v1/search?q=docker"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			t.Run(method+" "+path, func(t *testing.T) {
				backend := &fakeBackend{}

				rec := serve(t, backend, method, path)

				requireJSONError(t, rec, http.StatusMethodNotAllowed)
				require.Empty(t, backend.searchCalls)
				require.Empty(t, backend.labelsCalls)
			})
		}
	}
}

func TestHandler_UIServesIndexHTML(t *testing.T) {
	rec := serve(t, &fakeBackend{}, http.MethodGet, "/ui/")

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html"), rec.Header().Get("Content-Type"))
	require.Contains(t, strings.ToLower(rec.Body.String()), "<html")
}

func TestHandler_UIWithoutTrailingSlashRedirects(t *testing.T) {
	rec := serve(t, &fakeBackend{}, http.MethodGet, "/ui")

	require.GreaterOrEqual(t, rec.Code, 300)
	require.Less(t, rec.Code, 400)
	require.Equal(t, "/ui/", rec.Header().Get("Location"))
}
