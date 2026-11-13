package webui_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/sha1n/mcp-acdc-server/internal/webui"
	"github.com/sha1n/mcp-acdc-server/internal/webui/render"
)

const (
	testMaxResults = 7
	secretCause    = "s3cr3t-backend-failure-detail"
	expectedCSP    = "default-src 'self'; img-src 'self'; style-src 'self'; script-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
)

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
	results     []search.LabelledResult
	searchErr   error
	labels      []search.LabelCount
	labelsErr   error
	searchCalls []searchCall
	labelsCalls []labelsCall
	contexts    []context.Context
}

func (b *fakeBackend) LabelledSearch(ctx context.Context, query string, labels []string, limit int) ([]search.LabelledResult, error) {
	b.contexts = append(b.contexts, ctx)
	b.searchCalls = append(b.searchCalls, searchCall{query: query, labels: labels, limit: limit})
	return b.results, b.searchErr
}

func (b *fakeBackend) Labels(ctx context.Context, prefix string, limit int) ([]search.LabelCount, error) {
	b.contexts = append(b.contexts, ctx)
	b.labelsCalls = append(b.labelsCalls, labelsCall{prefix: prefix, limit: limit})
	return b.labels, b.labelsErr
}

type fakeSnapshot struct {
	docs      map[string]webui.Document
	docErr    error
	links     map[string]string
	requested []string
}

func (s *fakeSnapshot) Document(uri string) (webui.Document, error) {
	s.requested = append(s.requested, uri)
	if s.docErr != nil {
		return webui.Document{}, s.docErr
	}
	doc, ok := s.docs[uri]
	if !ok {
		return webui.Document{}, fmt.Errorf("lookup %q: %w", uri, webui.ErrDocumentNotFound)
	}
	return doc, nil
}

func (s *fakeSnapshot) ResolvePath(_, relPath string) (string, bool) {
	uri, ok := s.links[relPath]
	return uri, ok
}

func (s *fakeSnapshot) ResolveURI(uri string) (string, bool) {
	resolved, ok := s.links[uri]
	return resolved, ok
}

type fakeReader struct {
	snapshot  *fakeSnapshot
	snapshots int
	contexts  []context.Context
}

func (r *fakeReader) Snapshot(ctx context.Context) webui.DocumentSnapshot {
	r.snapshots++
	r.contexts = append(r.contexts, ctx)
	return r.snapshot
}

func newFakeReader() *fakeReader {
	return &fakeReader{snapshot: &fakeSnapshot{
		docs:  map[string]webui.Document{},
		links: map[string]string{"other.md": "acdc://other"},
	}}
}

func newHandler(t *testing.T, backend *fakeBackend, reader *fakeReader) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	webui.Mount(mux, backend, reader, config.UISettings{Enabled: true, MaxResults: testMaxResults})
	return mux
}

func serve(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func readStatic(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(path.Join("static", name))
	require.NoError(t, err)
	return data
}

func assertPageSecurityHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	assert.Equal(t, expectedCSP, rec.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
}

func assertAPIHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
}

func assertAPIError(t *testing.T, rec *httptest.ResponseRecorder, status int, message string) {
	t.Helper()
	assert.Equal(t, status, rec.Code)
	assertAPIHeaders(t, rec)
	assert.JSONEq(t, fmt.Sprintf(`{"error":%q}`, message), rec.Body.String())
}

func assertAPIBadRequest(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assertAPIHeaders(t, rec)
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Len(t, body, 1)
	assert.NotEmpty(t, body["error"])
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}

func TestMount_PanicsOnNilDependencies(t *testing.T) {
	settings := config.UISettings{Enabled: true, MaxResults: testMaxResults}
	assert.Panics(t, func() { webui.Mount(http.NewServeMux(), nil, newFakeReader(), settings) })
	assert.Panics(t, func() { webui.Mount(http.NewServeMux(), &fakeBackend{}, nil, settings) })
}

func TestPages_UIRedirectsToTrailingSlashKeepingQuery(t *testing.T) {
	h := newHandler(t, &fakeBackend{}, newFakeReader())
	cases := []struct {
		name, method, target, location string
	}{
		{"get without query", http.MethodGet, "/ui", "/ui/"},
		{"get with query", http.MethodGet, "/ui?q=docker&label=ops", "/ui/?q=docker&label=ops"},
		{"head", http.MethodHead, "/ui?q=x", "/ui/?q=x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(h, tc.method, tc.target)
			assert.Equal(t, http.StatusMovedPermanently, rec.Code)
			assert.Equal(t, tc.location, rec.Header().Get("Location"))
		})
	}
}

func TestPages_ServeEmbeddedPagesWithSecurityHeaders(t *testing.T) {
	h := newHandler(t, &fakeBackend{}, newFakeReader())
	cases := []struct {
		target, file string
	}{
		{"/ui/", "index.html"},
		{"/ui/?q=docker", "index.html"},
		{"/ui/doc", "doc.html"},
		{"/ui/doc?uri=acdc%3A%2F%2Fa", "doc.html"},
	}
	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			rec := serve(h, http.MethodGet, tc.target)
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, string(readStatic(t, tc.file)), rec.Body.String())
			assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
			assertPageSecurityHeaders(t, rec)
		})
	}
}

func TestPages_HeadReturnsHeadersWithoutBody(t *testing.T) {
	h := newHandler(t, &fakeBackend{}, newFakeReader())
	for _, target := range []string{"/ui/", "/ui/doc"} {
		t.Run(target, func(t *testing.T) {
			rec := serve(h, http.MethodHead, target)
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Empty(t, rec.Body.String())
			assertPageSecurityHeaders(t, rec)
		})
	}
}

func TestPages_ServeEveryEmbeddedAsset(t *testing.T) {
	h := newHandler(t, &fakeBackend{}, newFakeReader())
	err := fs.WalkDir(os.DirFS("static"), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || name == "index.html" {
			return err
		}
		t.Run(name, func(t *testing.T) {
			rec := serve(h, http.MethodGet, "/ui/"+name)
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, string(readStatic(t, name)), rec.Body.String())
		})
		return nil
	})
	require.NoError(t, err)
}

func TestPages_NoDirectoryListings(t *testing.T) {
	h := newHandler(t, &fakeBackend{}, newFakeReader())
	err := fs.WalkDir(os.DirFS("static"), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || name == "." {
			return err
		}
		t.Run(name, func(t *testing.T) {
			rec := serve(h, http.MethodGet, "/ui/"+name+"/")
			assert.Equal(t, http.StatusNotFound, rec.Code)
		})
		return nil
	})
	require.NoError(t, err)
}

func TestPages_UnknownPathIsNotFound(t *testing.T) {
	h := newHandler(t, &fakeBackend{}, newFakeReader())
	for _, target := range []string{"/ui/nope", "/ui/missing.js", "/ui/nested/dir/", "/ui/doc/extra"} {
		t.Run(target, func(t *testing.T) {
			rec := serve(h, http.MethodGet, target)
			assert.Equal(t, http.StatusNotFound, rec.Code)
		})
	}
}

func TestPages_NonGetMethodsAreNotAllowed(t *testing.T) {
	h := newHandler(t, &fakeBackend{}, newFakeReader())
	for _, target := range []string{"/ui", "/ui/", "/ui/doc"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			t.Run(method+" "+target, func(t *testing.T) {
				rec := serve(h, method, target)
				assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
				assert.Equal(t, "GET, HEAD", rec.Header().Get("Allow"))
			})
		}
	}
}

func TestAPI_UnknownPathIsNotFoundBeforeMethodCheck(t *testing.T) {
	h := newHandler(t, &fakeBackend{}, newFakeReader())
	cases := []struct{ method, target string }{
		{http.MethodGet, "/api/v1/"},
		{http.MethodGet, "/api/v1/nope"},
		{http.MethodGet, "/api/v1/search/extra"},
		{http.MethodPost, "/api/v1/nope"},
		{http.MethodDelete, "/api/v1/"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			assertAPIError(t, serve(h, tc.method, tc.target), http.StatusNotFound, "not found")
		})
	}
}

func TestAPI_NonGetMethodsAreNotAllowed(t *testing.T) {
	backend := &fakeBackend{}
	reader := newFakeReader()
	h := newHandler(t, backend, reader)
	for _, endpoint := range []string{"config", "labels", "search", "document"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodHead} {
			t.Run(method+" "+endpoint, func(t *testing.T) {
				rec := serve(h, method, "/api/v1/"+endpoint+"?q=x&uri=acdc%3A%2F%2Fa")
				assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
				assert.Equal(t, "GET", rec.Header().Get("Allow"))
				assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
				assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
				assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
				if method != http.MethodHead {
					assert.JSONEq(t, `{"error":"method not allowed"}`, rec.Body.String())
				}
			})
		}
	}
	assert.Empty(t, backend.searchCalls)
	assert.Empty(t, backend.labelsCalls)
	assert.Empty(t, reader.snapshot.requested)
}

func TestAPIConfig_ReturnsLimits(t *testing.T) {
	rec := serve(newHandler(t, &fakeBackend{}, newFakeReader()), http.MethodGet, "/api/v1/config")
	assert.Equal(t, http.StatusOK, rec.Code)
	assertAPIHeaders(t, rec)
	assert.JSONEq(t, fmt.Sprintf(`{"default_limit":%d,"max_limit":100}`, testMaxResults), rec.Body.String())
}

func TestAPILabels_PassesTrimmedPrefixAndLimit(t *testing.T) {
	cases := []struct {
		name, query, prefix string
		limit               int
	}{
		{"defaults", "", "", 10},
		{"trimmed prefix", "?q=%20%20Dep%20", "Dep", 10},
		{"empty limit is default", "?q=dep&limit=", "dep", 10},
		{"lower bound", "?limit=1", "", 1},
		{"upper bound", "?limit=100", "", 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeBackend{labels: []search.LabelCount{{Name: "deployment", Count: 12}, {Name: "docker", Count: 3}}}
			rec := serve(newHandler(t, backend, newFakeReader()), http.MethodGet, "/api/v1/labels"+tc.query)
			assert.Equal(t, http.StatusOK, rec.Code)
			assertAPIHeaders(t, rec)
			assert.JSONEq(t, `{"labels":[{"name":"deployment","count":12},{"name":"docker","count":3}]}`, rec.Body.String())
			assert.Equal(t, []labelsCall{{prefix: tc.prefix, limit: tc.limit}}, backend.labelsCalls)
		})
	}
}

func TestAPILabels_InvalidLimitIsBadRequest(t *testing.T) {
	for _, limit := range []string{"abc", "1.5", "0", "-1", "101", "%20"} {
		t.Run(limit, func(t *testing.T) {
			backend := &fakeBackend{}
			rec := serve(newHandler(t, backend, newFakeReader()), http.MethodGet, "/api/v1/labels?limit="+limit)
			assertAPIBadRequest(t, rec)
			assert.Empty(t, backend.labelsCalls)
		})
	}
}

func TestAPILabels_NilBackendResultIsEmptyArray(t *testing.T) {
	rec := serve(newHandler(t, &fakeBackend{}, newFakeReader()), http.MethodGet, "/api/v1/labels")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"labels":[]}`, rec.Body.String())
}

func TestAPILabels_BackendErrorIsOpaqueInternalError(t *testing.T) {
	backend := &fakeBackend{labelsErr: errors.New(secretCause)}
	rec := serve(newHandler(t, backend, newFakeReader()), http.MethodGet, "/api/v1/labels")
	assertAPIError(t, rec, http.StatusInternalServerError, "internal server error")
	assert.NotContains(t, rec.Body.String(), secretCause)
}

func TestAPISearch_NormalisesParameters(t *testing.T) {
	cases := []struct {
		name, query string
		want        searchCall
	}{
		{"trimmed query, default limit", "?q=%20docker%20", searchCall{"docker", []string{}, testMaxResults}},
		{"empty limit is default", "?q=docker&limit=", searchCall{"docker", []string{}, testMaxResults}},
		{"explicit limit", "?q=docker&limit=3", searchCall{"docker", []string{}, 3}},
		{"limit bounds low", "?q=docker&limit=1", searchCall{"docker", []string{}, 1}},
		{"limit bounds high", "?q=docker&limit=100", searchCall{"docker", []string{}, 100}},
		{
			"labels trimmed, lower-cased, empties dropped, deduped in order",
			"?q=docker&label=%20Ops%20&label=&label=deploy&label=OPS&label=%20%20&label=Deploy",
			searchCall{"docker", []string{"ops", "deploy"}, testMaxResults},
		},
		{"labels without query", "?label=Ops", searchCall{"", []string{"ops"}, testMaxResults}},
		{"blank query with labels", "?q=%20&label=ops&limit=5", searchCall{"", []string{"ops"}, 5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeBackend{}
			rec := serve(newHandler(t, backend, newFakeReader()), http.MethodGet, "/api/v1/search"+tc.query)
			assert.Equal(t, http.StatusOK, rec.Code)
			assertAPIHeaders(t, rec)
			require.Len(t, backend.searchCalls, 1)
			got := backend.searchCalls[0]
			assert.Equal(t, tc.want.query, got.query)
			assert.Equal(t, tc.want.labels, append([]string{}, got.labels...))
			assert.Equal(t, tc.want.limit, got.limit)
			assert.JSONEq(t, mustJSON(t, map[string]any{
				"query": tc.want.query, "labels": tc.want.labels, "limit": tc.want.limit, "total": 0, "results": []any{},
			}), rec.Body.String())
		})
	}
}

func TestAPISearch_InvalidLimitIsBadRequest(t *testing.T) {
	for _, limit := range []string{"abc", "2.5", "0", "-3", "101", "1e2"} {
		t.Run(limit, func(t *testing.T) {
			backend := &fakeBackend{}
			rec := serve(newHandler(t, backend, newFakeReader()), http.MethodGet, "/api/v1/search?q=docker&limit="+limit)
			assertAPIBadRequest(t, rec)
			assert.Empty(t, backend.searchCalls)
		})
	}
}

func TestAPISearch_EmptyQueryWithoutLabelsSkipsBackend(t *testing.T) {
	for _, query := range []string{"", "?q=", "?q=%20%20", "?label=&label=%20", "?q=%20&limit=4"} {
		t.Run(query, func(t *testing.T) {
			backend := &fakeBackend{results: []search.LabelledResult{{ChunkID: "unexpected"}}}
			rec := serve(newHandler(t, backend, newFakeReader()), http.MethodGet, "/api/v1/search"+query)
			assert.Equal(t, http.StatusOK, rec.Code)
			assertAPIHeaders(t, rec)
			assert.Empty(t, backend.searchCalls)
			var body struct {
				Query   *string          `json:"query"`
				Labels  []string         `json:"labels"`
				Total   *int             `json:"total"`
				Results []map[string]any `json:"results"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			require.NotNil(t, body.Query)
			assert.Empty(t, *body.Query)
			assert.NotNil(t, body.Labels)
			assert.Empty(t, body.Labels)
			require.NotNil(t, body.Total)
			assert.Zero(t, *body.Total)
			assert.NotNil(t, body.Results)
			assert.Empty(t, body.Results)
		})
	}
}

func TestAPISearch_MapsResults(t *testing.T) {
	results := []search.LabelledResult{
		{
			ChunkID:     "chunk-1",
			SourceURI:   "acdc://guides/deploy",
			ChunkURI:    "acdc://guides/deploy#docker~1",
			SourceTitle: "Deploy",
			SourcePath:  "guides/deploy.md",
			HeadingPath: []string{"Deploy", "Docker"},
			Labels:      []string{"guides", "deployment"},
			StartLine:   3,
			EndLine:     9,
			Score:       1.25,
			Snippet:     []search.SnippetSpan{{Text: "run "}, {Text: "docker", Hit: true}, {Text: " <b>now</b>"}},
			Content:     "## Docker\n\nSee [other](other.md).",
		},
		{
			ChunkID:    "chunk-2",
			SourceURI:  "acdc://intro",
			ChunkURI:   "acdc://intro#document",
			SourcePath: "intro.md",
			Content:    "plain",
		},
	}
	backend := &fakeBackend{results: results}
	reader := newFakeReader()
	rec := serve(newHandler(t, backend, reader), http.MethodGet, "/api/v1/search?q=docker&label=Deployment&limit=2")

	assert.Equal(t, http.StatusOK, rec.Code)
	assertAPIHeaders(t, rec)
	assert.Equal(t, 1, reader.snapshots)

	expected := make([]map[string]any, 0, len(results))
	for _, r := range results {
		expected = append(expected, map[string]any{
			"chunk_id":     r.ChunkID,
			"source_uri":   r.SourceURI,
			"chunk_uri":    r.ChunkURI,
			"title":        r.SourceTitle,
			"path":         r.SourcePath,
			"heading_path": nonNil(r.HeadingPath),
			"labels":       nonNil(r.Labels),
			"start_line":   r.StartLine,
			"end_line":     r.EndLine,
			"score":        r.Score,
			"document_url": render.ResultDocumentURL(r.SourceURI, r.ChunkURI),
			"snippet":      render.Snippet(r.Snippet),
			"content":      r.Content,
			"content_html": render.Markdown(r.Content, render.Options{FromPath: r.SourcePath, HeadingIDs: false, Links: reader.snapshot}),
		})
	}
	assert.JSONEq(t, mustJSON(t, map[string]any{
		"query": "docker", "labels": []string{"deployment"}, "limit": 2, "total": 2, "results": expected,
	}), rec.Body.String())
}

func TestAPISearch_NilBackendResultIsEmptyArray(t *testing.T) {
	rec := serve(newHandler(t, &fakeBackend{}, newFakeReader()), http.MethodGet, "/api/v1/search?q=nothing")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, fmt.Sprintf(`{"query":"nothing","labels":[],"limit":%d,"total":0,"results":[]}`, testMaxResults), rec.Body.String())
}

func TestAPISearch_BackendErrorIsOpaqueInternalError(t *testing.T) {
	backend := &fakeBackend{searchErr: errors.New(secretCause)}
	rec := serve(newHandler(t, backend, newFakeReader()), http.MethodGet, "/api/v1/search?q=docker")
	assertAPIError(t, rec, http.StatusInternalServerError, "internal server error")
	assert.NotContains(t, rec.Body.String(), secretCause)
}

func TestAPIDocument_ReturnsDocument(t *testing.T) {
	cases := []struct {
		name string
		doc  webui.Document
	}{
		{"with labels", webui.Document{
			URI: "acdc://guides/deploy", Title: "Deploy", Path: "guides/deploy.md",
			Labels: []string{"guides", "deployment"}, Content: "# Deploy\n\nSee [other](other.md).",
		}},
		{"nil labels", webui.Document{URI: "acdc://intro", Title: "Intro", Path: "intro.md", Content: "plain"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := newFakeReader()
			reader.snapshot.docs[tc.doc.URI] = tc.doc
			rec := serve(newHandler(t, &fakeBackend{}, reader), http.MethodGet, "/api/v1/document?uri="+url.QueryEscape(tc.doc.URI))

			assert.Equal(t, http.StatusOK, rec.Code)
			assertAPIHeaders(t, rec)
			assert.Equal(t, 1, reader.snapshots)
			assert.Equal(t, []string{tc.doc.URI}, reader.snapshot.requested)
			assert.JSONEq(t, mustJSON(t, map[string]any{
				"uri":     tc.doc.URI,
				"title":   tc.doc.Title,
				"path":    tc.doc.Path,
				"labels":  nonNil(tc.doc.Labels),
				"content": tc.doc.Content,
				"content_html": render.Markdown(tc.doc.Content, render.Options{
					FromPath: tc.doc.Path, HeadingIDs: true, Links: reader.snapshot,
				}),
			}), rec.Body.String())
		})
	}
}

func TestAPIDocument_BlankURIIsBadRequest(t *testing.T) {
	for _, query := range []string{"", "?uri=", "?uri=%20%20"} {
		t.Run(query, func(t *testing.T) {
			reader := newFakeReader()
			rec := serve(newHandler(t, &fakeBackend{}, reader), http.MethodGet, "/api/v1/document"+query)
			assertAPIError(t, rec, http.StatusBadRequest, "uri is required")
			assert.Empty(t, reader.snapshot.requested)
		})
	}
}

func TestAPIDocument_UnknownURIIsNotFound(t *testing.T) {
	rec := serve(newHandler(t, &fakeBackend{}, newFakeReader()), http.MethodGet, "/api/v1/document?uri=acdc%3A%2F%2Fmissing")
	assertAPIError(t, rec, http.StatusNotFound, "document not found")
}

func TestAPIDocument_SnapshotErrorIsOpaqueInternalError(t *testing.T) {
	reader := newFakeReader()
	reader.snapshot.docErr = errors.New(secretCause)
	rec := serve(newHandler(t, &fakeBackend{}, reader), http.MethodGet, "/api/v1/document?uri=acdc%3A%2F%2Fa")
	assertAPIError(t, rec, http.StatusInternalServerError, "internal server error")
	assert.NotContains(t, rec.Body.String(), secretCause)
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

type requestContextKey struct{}

func TestAPI_PassesRequestContextToBackendAndDocuments(t *testing.T) {
	tests := []struct {
		name            string
		target          string
		wantBackendCtxs int
		wantReaderCtxs  int
	}{
		{name: "search", target: "/api/v1/search?q=docker", wantBackendCtxs: 1, wantReaderCtxs: 1},
		{name: "labels", target: "/api/v1/labels?q=dep", wantBackendCtxs: 1, wantReaderCtxs: 0},
		{name: "document", target: "/api/v1/document?uri=acdc%3A%2F%2Fdocs%2Fa", wantBackendCtxs: 0, wantReaderCtxs: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &fakeBackend{results: []search.LabelledResult{{SourceURI: "acdc://docs/a"}}}
			reader := newFakeReader()
			reader.snapshot.docs["acdc://docs/a"] = webui.Document{URI: "acdc://docs/a", Path: "docs/a.md", Content: "# A\n"}
			ctx := context.WithValue(context.Background(), requestContextKey{}, tt.name)
			req := httptest.NewRequest(http.MethodGet, tt.target, nil).WithContext(ctx)
			rec := httptest.NewRecorder()

			newHandler(t, backend, reader).ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Len(t, backend.contexts, tt.wantBackendCtxs)
			require.Len(t, reader.contexts, tt.wantReaderCtxs)
			for _, got := range append(backend.contexts, reader.contexts...) {
				require.Equal(t, tt.name, got.Value(requestContextKey{}), "request context must reach the backend")
			}
		})
	}
}
