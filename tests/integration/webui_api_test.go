package integration

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

type webUIResult struct {
	ChunkID     string   `json:"chunk_id"`
	SourceURI   string   `json:"source_uri"`
	ChunkURI    string   `json:"chunk_uri"`
	Title       string   `json:"title"`
	Path        string   `json:"path"`
	HeadingPath []string `json:"heading_path"`
	Labels      []string `json:"labels"`
	DocumentURL string   `json:"document_url"`
	Snippet     string   `json:"snippet"`
	Content     string   `json:"content"`
	ContentHTML string   `json:"content_html"`
}

type webUISearchResponse struct {
	Query   string        `json:"query"`
	Labels  []string      `json:"labels"`
	Limit   int           `json:"limit"`
	Total   int           `json:"total"`
	Results []webUIResult `json:"results"`
}

type webUIDocument struct {
	URI         string   `json:"uri"`
	Title       string   `json:"title"`
	Path        string   `json:"path"`
	Labels      []string `json:"labels"`
	Content     string   `json:"content"`
	ContentHTML string   `json:"content_html"`
}

var (
	documentHrefRe = regexp.MustCompile(`href="(/ui/doc\?uri=[^"]*)"`)
	headingIDRe    = regexp.MustCompile(`<h[1-6][^>]*\sid="([^"]+)"`)
)

const webUIFiller = "filler words keep this section long enough to be split into several parts by the chunker. "

func webUIFixture() map[string]string {
	return map[string]string{
		"guide.md": "---\nname: Guide\ndescription: Guide\nkeywords:\n  - deployment\n---\n" +
			"Intro preambletoken text before any heading.\n\n" +
			"# Guide\n\n" +
			"Read [Other](other.md), [Other setup](other.md#setup) and [Nested](sub/nested.md). " +
			"Also [Missing](missing.md), [site](https://example.com/page) and [jump](#guide).\n\n" +
			"## Big\n\n" +
			strings.Repeat(webUIFiller, 20) + "\n\n" +
			strings.Repeat(webUIFiller, 20) + "\n\n" +
			"zebrapart " + strings.Repeat(webUIFiller, 20) + "\n",
		"other.md": "---\nname: Other\ndescription: Other\nkeywords:\n  - deployment\n---\n" +
			"# Other\n\nOther body.\n\n## Setup\n\nSetup steps, back to [Guide](guide.md).\n",
		"sub/nested.md": "---\nname: Nested\ndescription: Nested\n---\n" +
			"# Nested\n\nUp to [Guide](../guide.md#big).\n",
		"hostile.md": "---\nname: Hostile\ndescription: Hostile\n---\n" +
			"# Hostile\n\n<script>alert('pwned')</script>\n\n" +
			"<img src=x onerror=alert(1)>\n\n" +
			"A [bad link](javascript:alert(1)) and literal <mark>hostiletoken</mark> text.\n",
	}
}

func getJSON(t *testing.T, rawURL string, out any) int {
	t.Helper()
	resp, err := http.Get(rawURL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	if resp.StatusCode == http.StatusOK && out != nil {
		require.NoError(t, json.Unmarshal(body, out), string(body))
	}
	return resp.StatusCode
}

func webUISearch(t *testing.T, baseURL string, params url.Values) webUISearchResponse {
	t.Helper()
	var out webUISearchResponse
	require.Equal(t, http.StatusOK, getJSON(t, baseURL+"/api/v1/search?"+params.Encode(), &out))
	return out
}

func webUIDocumentByURI(t *testing.T, baseURL, uri string) webUIDocument {
	t.Helper()
	var out webUIDocument
	status := getJSON(t, baseURL+"/api/v1/document?uri="+url.QueryEscape(uri), &out)
	require.Equal(t, http.StatusOK, status, "document %q", uri)
	return out
}

// parseDocumentURL splits a server-built document URL into source URI and fragment.
func parseDocumentURL(t *testing.T, documentURL string) (uri, fragment string) {
	t.Helper()
	parsed, err := url.Parse(documentURL)
	require.NoError(t, err)
	require.Equal(t, "/ui/doc", parsed.Path, documentURL)
	uri = parsed.Query().Get("uri")
	require.NotEmpty(t, uri, documentURL)
	return uri, parsed.Fragment
}

func documentHrefs(contentHTML string) []string {
	var hrefs []string
	for _, m := range documentHrefRe.FindAllStringSubmatch(contentHTML, -1) {
		hrefs = append(hrefs, html.UnescapeString(m[1]))
	}
	return hrefs
}

func headingIDs(contentHTML string) []string {
	var ids []string
	for _, m := range headingIDRe.FindAllStringSubmatch(contentHTML, -1) {
		ids = append(ids, html.UnescapeString(m[1]))
	}
	return ids
}

// requireDocumentLinksResolve asserts every rewritten document link is fetchable and
// that any fragment it carries names a heading id in the target document.
func requireDocumentLinksResolve(t *testing.T, baseURL, contentHTML string) {
	t.Helper()
	for _, href := range documentHrefs(contentHTML) {
		uri, fragment := parseDocumentURL(t, href)
		target := webUIDocumentByURI(t, baseURL, uri)
		if fragment != "" {
			require.Contains(t, headingIDs(target.ContentHTML), fragment, "link %s", href)
		}
	}
}

func findResultByChunkSuffix(t *testing.T, results []webUIResult, suffix string) webUIResult {
	t.Helper()
	for _, r := range results {
		if strings.HasSuffix(r.ChunkURI, suffix) {
			return r
		}
	}
	require.Failf(t, "result not found", "no result with chunk_uri suffix %q in %d results", suffix, len(results))
	return webUIResult{}
}

func forEachCrossRefMode(t *testing.T, fn func(t *testing.T, baseURL string)) {
	for _, crossRef := range []bool{false, true} {
		t.Run(fmt.Sprintf("cross-ref=%t", crossRef), func(t *testing.T) {
			baseURL, _ := testkit.StartSSE(t,
				&testkit.ContentDirOptions{Resources: webUIFixture()},
				&testkit.FlagOptions{UIEnabled: true, CrossRef: crossRef})
			fn(t, baseURL)
		})
	}
}

func TestWebUIAPI_DocumentLinksResolveToFetchableDocuments(t *testing.T) {
	forEachCrossRefMode(t, func(t *testing.T, baseURL string) {
		guide := webUIDocumentByURI(t, baseURL, "acdc://guide")

		var targets []string
		for _, href := range documentHrefs(guide.ContentHTML) {
			uri, fragment := parseDocumentURL(t, href)
			targets = append(targets, uri+"#"+fragment)
		}
		require.ElementsMatch(t, []string{"acdc://other#", "acdc://other#setup", "acdc://sub/nested#"}, targets)
		requireDocumentLinksResolve(t, baseURL, guide.ContentHTML)

		nested := webUIDocumentByURI(t, baseURL, "acdc://sub/nested")
		require.Len(t, documentHrefs(nested.ContentHTML), 1)
		requireDocumentLinksResolve(t, baseURL, nested.ContentHTML)
	})
}

func TestWebUIAPI_NonDocumentLinks(t *testing.T) {
	forEachCrossRefMode(t, func(t *testing.T, baseURL string) {
		guide := webUIDocumentByURI(t, baseURL, "acdc://guide")

		require.Contains(t, guide.ContentHTML, `<span class="broken-link" title="Broken link: missing.md">Missing</span>`)
		require.Regexp(t, `<a href="https://example.com/page"[^>]*target="_blank"[^>]*>site</a>`, guide.ContentHTML)
		require.Regexp(t, `<a href="https://example.com/page"[^>]*rel="noopener noreferrer"`, guide.ContentHTML)
		require.Contains(t, guide.ContentHTML, `<a href="#guide">jump</a>`)
		require.NotContains(t, guide.ContentHTML, `href="missing.md"`)
	})
}

func TestWebUIAPI_DocumentHeadingIDs(t *testing.T) {
	forEachCrossRefMode(t, func(t *testing.T, baseURL string) {
		guide := webUIDocumentByURI(t, baseURL, "acdc://guide")

		require.Equal(t, []string{"guide", "big"}, headingIDs(guide.ContentHTML))
		require.Equal(t, "acdc://guide", guide.URI)
		require.Equal(t, "Guide", guide.Title)
		require.Equal(t, []string{"guide", "deployment"}, guide.Labels)
	})
}

func TestWebUIAPI_ResultDocumentURLFragmentsMatchHeadingIDs(t *testing.T) {
	forEachCrossRefMode(t, func(t *testing.T, baseURL string) {
		resp := webUISearch(t, baseURL, url.Values{"label": {"deployment"}, "limit": {"100"}})

		require.NotEmpty(t, resp.Results)
		sources := map[string]bool{}
		for _, r := range resp.Results {
			uri, fragment := parseDocumentURL(t, r.DocumentURL)
			require.Equal(t, r.SourceURI, uri)
			sources[uri] = true
			if fragment != "" {
				doc := webUIDocumentByURI(t, baseURL, uri)
				require.Contains(t, headingIDs(doc.ContentHTML), fragment, "result %s", r.ChunkURI)
			}
			requireDocumentLinksResolve(t, baseURL, r.ContentHTML)
			require.NotContains(t, r.ContentHTML, " id=", "result fragments carry no heading ids")
		}
		require.Equal(t, map[string]bool{"acdc://guide": true, "acdc://other": true}, sources)
	})
}

func TestWebUIAPI_SplitPartResolvesToSectionHeading(t *testing.T) {
	forEachCrossRefMode(t, func(t *testing.T, baseURL string) {
		resp := webUISearch(t, baseURL, url.Values{"q": {"zebrapart"}})

		part := findResultByChunkSuffix(t, resp.Results, "#big~2")
		uri, fragment := parseDocumentURL(t, part.DocumentURL)
		require.Equal(t, "acdc://guide", uri)
		require.Equal(t, "big", fragment)
	})
}

func TestWebUIAPI_PreambleResultHasNoFragment(t *testing.T) {
	forEachCrossRefMode(t, func(t *testing.T, baseURL string) {
		resp := webUISearch(t, baseURL, url.Values{"q": {"preambletoken"}})

		require.NotEmpty(t, resp.Results)
		preamble := resp.Results[0]
		require.Equal(t, "acdc://guide", preamble.SourceURI)
		require.NotContains(t, preamble.DocumentURL, "#")
		require.Equal(t, "/ui/doc?uri="+url.QueryEscape("acdc://guide"), preamble.DocumentURL)
	})
}

func TestWebUIAPI_HostileContentIsNeutralised(t *testing.T) {
	baseURL, _ := testkit.StartSSE(t,
		&testkit.ContentDirOptions{Resources: webUIFixture()},
		&testkit.FlagOptions{UIEnabled: true})

	resp := webUISearch(t, baseURL, url.Values{"q": {"hostiletoken"}})
	require.NotEmpty(t, resp.Results)
	result := resp.Results[0]
	require.Equal(t, "acdc://hostile", result.SourceURI)
	require.Contains(t, result.Snippet, "&lt;mark&gt;")
	require.Contains(t, result.Snippet, "<mark>hostiletoken</mark>")
	require.Equal(t, 1, strings.Count(result.Snippet, "<mark>"), "only the real hit is highlighted: %s", result.Snippet)
	require.NotContains(t, result.Snippet, "<script")

	doc := webUIDocumentByURI(t, baseURL, "acdc://hostile")
	for _, contentHTML := range []string{doc.ContentHTML, result.ContentHTML} {
		require.NotContains(t, contentHTML, "<script")
		require.NotContains(t, contentHTML, "<img")
		require.NotContains(t, contentHTML, "onerror")
		require.NotContains(t, contentHTML, `href="javascript:`)
		require.NotContains(t, contentHTML, "<mark>")
		require.Contains(t, contentHTML, `<span class="broken-link"`)
	}
}

func TestWebUIAPI_DisabledRoutesAreNotFound(t *testing.T) {
	baseURL, _ := testkit.StartSSE(t, &testkit.ContentDirOptions{Resources: webUIFixture()}, nil)

	for _, path := range []string{"/ui", "/ui/", "/ui/doc?uri=acdc://guide", "/api/v1/config", "/api/v1/search?q=guide", "/api/v1/labels", "/api/v1/document?uri=acdc://guide"} {
		require.Equal(t, http.StatusNotFound, getJSON(t, baseURL+path, nil), path)
	}
	requireHealthUnchanged(t, baseURL)
}

func requireHealthUnchanged(t *testing.T, baseURL string) {
	t.Helper()
	resp, err := http.Get(baseURL + "/health")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	require.Equal(t, "ok", string(body))
}

func TestWebUIAPI_RoutesRequireAuth(t *testing.T) {
	tests := []struct {
		name      string
		authType  string
		flags     map[string]string
		authorize func(*http.Request)
	}{
		{
			name:      "basic",
			authType:  "basic",
			flags:     map[string]string{"auth-basic-username": "user", "auth-basic-password": "password"},
			authorize: func(r *http.Request) { r.SetBasicAuth("user", "password") },
		},
		{
			name:      "apikey",
			authType:  "apikey",
			flags:     map[string]string{"auth-api-keys": "key-A"},
			authorize: func(r *http.Request) { r.Header.Set("X-API-Key", "key-A") },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{Resources: webUIFixture()})
			flags := testkit.NewTestFlags(t, contentDir, &testkit.FlagOptions{AuthType: tt.authType, UIEnabled: true})
			for name, value := range tt.flags {
				require.NoError(t, flags.Set(name, value))
			}
			env := testkit.NewTestEnv(testkit.NewACDCService("acdc", flags))
			props, err := env.Start()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, env.Stop()) })
			baseURL := props["acdc.baseURL"].(string)

			for _, path := range []string{"/ui/", "/ui/doc?uri=acdc://guide", "/api/v1/config", "/api/v1/search?q=guide", "/api/v1/labels", "/api/v1/document?uri=acdc://guide"} {
				require.Equal(t, http.StatusUnauthorized, doGet(t, baseURL+path, func(*http.Request) {}), "unauthenticated %s", path)
				require.Equal(t, http.StatusOK, doGet(t, baseURL+path, tt.authorize), "authenticated %s", path)
			}
			requireHealthUnchanged(t, baseURL)
		})
	}
}

func doGet(t *testing.T, rawURL string, authorize func(*http.Request)) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	require.NoError(t, err)
	authorize(req)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	// A body closed unread is drained in the background, so the next request can
	// dial a second connection that then sits in the pool without ever being used.
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	return resp.StatusCode
}
