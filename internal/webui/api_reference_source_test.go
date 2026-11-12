package webui_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/sha1n/mcp-acdc-server/internal/webui"
)

const (
	guideURI     = "acdc://docs/guide"
	guidePath    = "docs/guide.md"
	guideContent = "# Guide\n\n## Setup\n\nSee [Details][d].\n\n## Usage\n\nAlso [Details][d].\n\n## Details\n\nBody.\n\n[d]: #details\n"
	notesURI     = "acdc://docs/notes"
	notesPath    = "docs/notes.md"
	notesContent = "# Notes\n\n## Intro\n\nRead [More][m].\n\n## More\n\n[m]: #more\n"
)

func guideChunk(id, content string) search.LabelledResult {
	return search.LabelledResult{
		ChunkID: guideURI + "#" + id, SourceURI: guideURI, ChunkURI: guideURI + "#" + id,
		SourcePath: guidePath, Content: content,
	}
}

func searchContentHTML(t *testing.T, backend *fakeBackend, reader *fakeReader) []string {
	t.Helper()
	rec := serve(newHandler(t, backend, reader), http.MethodGet, "/api/v1/search?q=details")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Results []struct {
			ContentHTML string `json:"content_html"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	html := make([]string, 0, len(body.Results))
	for _, r := range body.Results {
		html = append(html, r.ContentHTML)
	}
	return html
}

func documentReadCounts(snapshot *fakeSnapshot) map[string]int {
	counts := map[string]int{}
	for _, uri := range snapshot.requested {
		counts[uri]++
	}
	return counts
}

func TestSearchAPI_ContentHTMLResolvesReferenceDefinitionFromSourceDocument(t *testing.T) {
	backend := &fakeBackend{results: []search.LabelledResult{guideChunk("setup", "## Setup\n\nSee [Details][d].\n")}}
	reader := newFakeReader()
	reader.snapshot.docs[guideURI] = webui.Document{URI: guideURI, Path: guidePath, Content: guideContent}

	html := searchContentHTML(t, backend, reader)

	require.Len(t, html, 1)
	require.Contains(t, html[0], `href="/ui/doc?uri=acdc%3A%2F%2Fdocs%2Fguide#details"`)
	require.NotContains(t, html[0], "[Details][d]")
	require.NotContains(t, html[0], "Body.", "the source document contributes definitions only")
}

func TestSearchAPI_ContentHTMLRendersChunkAloneWhenSourceDocumentMissing(t *testing.T) {
	backend := &fakeBackend{results: []search.LabelledResult{guideChunk("setup", "## Setup\n\nSee [Details][d] and [Install][i].\n\n[i]: #install\n")}}
	reader := newFakeReader()

	html := searchContentHTML(t, backend, reader)

	require.Len(t, html, 1)
	require.Contains(t, html[0], "[Details][d]")
	require.Contains(t, html[0], `href="/ui/doc?uri=acdc%3A%2F%2Fdocs%2Fguide#install"`)
	require.Equal(t, map[string]int{guideURI: 1}, documentReadCounts(reader.snapshot))
}

func TestSearchAPI_ContentHTMLReadsEachSourceDocumentOncePerRequest(t *testing.T) {
	backend := &fakeBackend{results: []search.LabelledResult{
		guideChunk("setup", "## Setup\n\nSee [Details][d].\n"),
		{
			ChunkID: notesURI + "#intro", SourceURI: notesURI, ChunkURI: notesURI + "#intro",
			SourcePath: notesPath, Content: "## Intro\n\nRead [More][m].\n",
		},
		guideChunk("usage", "## Usage\n\nAlso [Details][d].\n"),
	}}
	reader := newFakeReader()
	reader.snapshot.docs[guideURI] = webui.Document{URI: guideURI, Path: guidePath, Content: guideContent}
	reader.snapshot.docs[notesURI] = webui.Document{URI: notesURI, Path: notesPath, Content: notesContent}

	html := searchContentHTML(t, backend, reader)

	require.Len(t, html, 3)
	require.Contains(t, html[0], `href="/ui/doc?uri=acdc%3A%2F%2Fdocs%2Fguide#details"`)
	require.Contains(t, html[1], `href="/ui/doc?uri=acdc%3A%2F%2Fdocs%2Fnotes#more"`)
	require.Contains(t, html[2], `href="/ui/doc?uri=acdc%3A%2F%2Fdocs%2Fguide#details"`)
	require.Equal(t, 1, reader.snapshots, "one snapshot per request")
	require.Equal(t, map[string]int{guideURI: 1, notesURI: 1}, documentReadCounts(reader.snapshot))
}
