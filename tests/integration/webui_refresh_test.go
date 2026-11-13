package integration

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

type webUILabel struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type webUILabelsResponse struct {
	Labels []webUILabel `json:"labels"`
}

// webUIRefreshState is what the web UI API reports for one document, label and
// search term at a single instant, so the before/after expectations of each
// change can be compared as plain values.
type webUIRefreshState struct {
	SearchHit       bool
	LabelPresent    bool
	DocumentStatus  int
	DocumentHasTerm bool
}

func webUIRefreshDoc(label, term string) string {
	return "---\nname: Refresh\ndescription: Refresh\nkeywords:\n  - " + label + "\n---\n" +
		"# Refresh\n\nThe body mentions " + term + " exactly once.\n"
}

// observeWebUI must not call require: it runs inside require.Eventually's
// polling goroutine, where FailNow cannot stop the test.
func observeWebUI(baseURL, uri, label, term string) webUIRefreshState {
	var state webUIRefreshState

	var found webUISearchResponse
	if fetchJSON(baseURL+"/api/v1/search?"+url.Values{"q": {term}}.Encode(), &found) == http.StatusOK {
		state.SearchHit = slices.ContainsFunc(found.Results, func(r webUIResult) bool { return r.SourceURI == uri })
	}

	var labels webUILabelsResponse
	if fetchJSON(baseURL+"/api/v1/labels", &labels) == http.StatusOK {
		state.LabelPresent = slices.ContainsFunc(labels.Labels, func(l webUILabel) bool { return l.Name == label })
	}

	var doc webUIDocument
	state.DocumentStatus = fetchJSON(baseURL+"/api/v1/document?uri="+url.QueryEscape(uri), &doc)
	state.DocumentHasTerm = state.DocumentStatus == http.StatusOK && strings.Contains(doc.Content, term)
	return state
}

// fetchJSON reports 0 when the request or decoding fails.
func fetchJSON(rawURL string, out any) int {
	resp, err := http.Get(rawURL)
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(out) != nil {
		return 0
	}
	return resp.StatusCode
}

// TestWebUIRefresh_ReflectsContentChangesWithoutMCPRequests proves the web UI
// API revalidates the catalog itself: no MCP request is ever made, so only the
// web UI requests polled here can drive the debounced refresh that makes an
// edited, added or deleted document visible through search, labels and document.
func TestWebUIRefresh_ReflectsContentChangesWithoutMCPRequests(t *testing.T) {
	present := webUIRefreshState{SearchHit: true, LabelPresent: true, DocumentStatus: http.StatusOK, DocumentHasTerm: true}
	absent := webUIRefreshState{DocumentStatus: http.StatusNotFound}

	tests := []struct {
		name    string
		initial map[string]string
		mutate  func(t *testing.T, resourcesDir string)
		uri     string
		label   string
		term    string
		before  webUIRefreshState
		after   webUIRefreshState
	}{
		{
			name:    "edited document",
			initial: map[string]string{"edited.md": webUIRefreshDoc("oldlabel", "oldterm")},
			mutate: func(t *testing.T, dir string) {
				writeFile(t, dir, "edited.md", webUIRefreshDoc("freshlabel", "freshterm"))
			},
			uri:    "acdc://edited",
			label:  "freshlabel",
			term:   "freshterm",
			before: webUIRefreshState{DocumentStatus: http.StatusOK},
			after:  present,
		},
		{
			name:    "added document",
			initial: map[string]string{"stable.md": webUIRefreshDoc("stablelabel", "stableterm")},
			mutate: func(t *testing.T, dir string) {
				writeFile(t, dir, "added.md", webUIRefreshDoc("addedlabel", "addedterm"))
			},
			uri:    "acdc://added",
			label:  "addedlabel",
			term:   "addedterm",
			before: absent,
			after:  present,
		},
		{
			name: "deleted document",
			initial: map[string]string{
				"stable.md": webUIRefreshDoc("stablelabel", "stableterm"),
				"doomed.md": webUIRefreshDoc("doomedlabel", "doomedterm"),
			},
			mutate: func(t *testing.T, dir string) {
				require.NoError(t, os.Remove(filepath.Join(dir, "doomed.md")))
			},
			uri:    "acdc://doomed",
			label:  "doomedlabel",
			term:   "doomedterm",
			before: present,
			after:  absent,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{Resources: tt.initial})
			env := testkit.NewTestEnv(testkit.NewACDCService("acdc-sse",
				testkit.NewTestFlags(t, contentDir, &testkit.FlagOptions{UIEnabled: true})))
			props, err := env.Start()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, env.Stop()) })
			baseURL := props["acdc.baseURL"].(string)

			require.Equal(t, tt.before, observeWebUI(baseURL, tt.uri, tt.label, tt.term))

			tt.mutate(t, content.NewContentProvider(contentDir).ResourcesDir)

			var last webUIRefreshState
			require.Eventually(t, func() bool {
				last = observeWebUI(baseURL, tt.uri, tt.label, tt.term)
				return last == tt.after
			}, refreshTimeout, refreshTick, "web UI API never reflected the change")
			require.Equal(t, tt.after, last)
		})
	}
}
