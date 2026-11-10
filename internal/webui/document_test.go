package webui

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const testDocURI = "acdc://reference/search-features"

type fakeDocumentReader struct {
	docs  map[string]Document
	err   error
	calls []string
}

func (f *fakeDocumentReader) Document(uri string) (Document, error) {
	f.calls = append(f.calls, uri)
	if f.err != nil {
		return Document{}, f.err
	}
	doc, ok := f.docs[uri]
	if !ok {
		return Document{}, ErrDocumentNotFound
	}
	return doc, nil
}

func newDocumentReader(docs ...Document) *fakeDocumentReader {
	reader := &fakeDocumentReader{docs: map[string]Document{}}
	for _, doc := range docs {
		reader.docs[doc.URI] = doc
	}
	return reader
}

func serveDocument(t *testing.T, opts []HandlerOption, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	NewHandler(&fakeBackend{}, testUISettings(), opts...).ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func documentTarget(uri string) string {
	return "/api/v1/document?uri=" + url.QueryEscape(uri)
}

func TestHandler_Document_ShapeAndValues(t *testing.T) {
	reader := newDocumentReader(Document{
		URI:     testDocURI,
		Title:   "Search Features",
		Path:    "reference/search-features.md",
		Content: "# Search Features\n\nBody text.\n",
		Labels:  []string{"reference", "search"},
	})

	rec := serveDocument(t, []HandlerOption{WithDocumentReader(reader)}, http.MethodGet, documentTarget(testDocURI))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, jsonContentType, rec.Header().Get("Content-Type"))
	var raw map[string]any
	decode(t, rec, &raw)
	require.ElementsMatch(t, []string{"uri", "title", "path", "labels", "content", "content_html"}, keys(raw))

	var body struct {
		URI         string   `json:"uri"`
		Title       string   `json:"title"`
		Path        string   `json:"path"`
		Labels      []string `json:"labels"`
		Content     string   `json:"content"`
		ContentHTML string   `json:"content_html"`
	}
	decode(t, rec, &body)
	require.Equal(t, testDocURI, body.URI)
	require.Equal(t, "Search Features", body.Title)
	require.Equal(t, "reference/search-features.md", body.Path)
	require.Equal(t, []string{"reference", "search"}, body.Labels)
	require.Equal(t, "# Search Features\n\nBody text.\n", body.Content)
	require.Contains(t, body.ContentHTML, "<h1")
	require.Contains(t, body.ContentHTML, "<p>Body text.</p>")
	require.Equal(t, []string{testDocURI}, reader.calls)
}

func TestHandler_Document_MissingOrEmptyURIIsBadRequest(t *testing.T) {
	for _, target := range []string{"/api/v1/document", "/api/v1/document?uri="} {
		t.Run(target, func(t *testing.T) {
			reader := newDocumentReader()

			rec := serveDocument(t, []HandlerOption{WithDocumentReader(reader)}, http.MethodGet, target)

			requireJSONError(t, rec, http.StatusBadRequest)
			require.Empty(t, reader.calls)
		})
	}
}

func TestHandler_Document_UnknownURIIsNotFound(t *testing.T) {
	cases := map[string]error{
		"sentinel": ErrDocumentNotFound,
		"wrapped":  fmt.Errorf("lookup %q: %w", testDocURI, ErrDocumentNotFound),
	}
	for name, readErr := range cases {
		t.Run(name, func(t *testing.T) {
			reader := &fakeDocumentReader{err: readErr}

			rec := serveDocument(t, []HandlerOption{WithDocumentReader(reader)}, http.MethodGet, documentTarget(testDocURI))

			requireJSONError(t, rec, http.StatusNotFound)
			require.Equal(t, []string{testDocURI}, reader.calls)
		})
	}
}

func TestHandler_Document_ReadFailureIsInternalError(t *testing.T) {
	reader := &fakeDocumentReader{err: errors.New("disk on fire")}

	rec := serveDocument(t, []HandlerOption{WithDocumentReader(reader)}, http.MethodGet, documentTarget(testDocURI))

	requireJSONError(t, rec, http.StatusInternalServerError)
	require.NotContains(t, rec.Body.String(), "disk on fire")
}

func TestHandler_Document_NoReaderWiredIsNotFound(t *testing.T) {
	rec := serveDocument(t, nil, http.MethodGet, documentTarget(testDocURI))

	requireJSONError(t, rec, http.StatusNotFound)
}

func TestHandler_Document_RejectsNonGET(t *testing.T) {
	reader := newDocumentReader(Document{URI: testDocURI})

	rec := serveDocument(t, []HandlerOption{WithDocumentReader(reader)}, http.MethodPost, documentTarget(testDocURI))

	requireJSONError(t, rec, http.StatusMethodNotAllowed)
	require.Equal(t, http.MethodGet, rec.Header().Get("Allow"))
	require.Empty(t, reader.calls)
}

func TestHandler_Document_ContentHTMLIsRenderedMarkdownWithoutRawHTML(t *testing.T) {
	content := "# Title\n\n<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n[link](javascript:alert(1))\n"
	reader := newDocumentReader(Document{URI: testDocURI, Content: content})

	rec := serveDocument(t, []HandlerOption{WithDocumentReader(reader)}, http.MethodGet, documentTarget(testDocURI))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Content     string `json:"content"`
		ContentHTML string `json:"content_html"`
	}
	decode(t, rec, &body)
	require.Equal(t, content, body.Content)
	require.Contains(t, body.ContentHTML, "<h1")
	lower := strings.ToLower(body.ContentHTML)
	require.NotContains(t, lower, "<script")
	require.NotContains(t, lower, "<img")
	require.NotContains(t, lower, "onerror")
	require.NotContains(t, lower, "javascript:")
}

func TestHandler_Document_LabelsAreNeverNull(t *testing.T) {
	reader := newDocumentReader(Document{URI: testDocURI, Title: "No labels", Content: "text"})

	rec := serveDocument(t, []HandlerOption{WithDocumentReader(reader)}, http.MethodGet, documentTarget(testDocURI))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"labels":[]`)
}
