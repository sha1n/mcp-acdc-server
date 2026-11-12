package webui_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sha1n/mcp-acdc-server/internal/search"
)

func TestSearchAPI_ContentHTMLQualifiesFragmentLinksWithSourceDocument(t *testing.T) {
	backend := &fakeBackend{results: []search.LabelledResult{{
		ChunkID:    "acdc://docs/guide#setup",
		SourceURI:  "acdc://docs/guide",
		ChunkURI:   "acdc://docs/guide#setup",
		SourcePath: "docs/guide.md",
		Content:    "## Setup\n\nSee [Install](#install).\n",
	}}}

	rec := serve(newHandler(t, backend, newFakeReader()), http.MethodGet, "/api/v1/search?q=setup")

	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Results []struct {
			ContentHTML string `json:"content_html"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Results, 1)
	require.Contains(t, body.Results[0].ContentHTML, `href="/ui/doc?uri=acdc%3A%2F%2Fdocs%2Fguide#install"`)
	require.NotContains(t, body.Results[0].ContentHTML, `href="#install"`)
}
