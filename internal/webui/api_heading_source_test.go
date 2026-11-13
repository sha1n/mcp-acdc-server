package webui_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sha1n/mcp-acdc-server/internal/webui"
)

func TestDocumentAPI_HeadingIDsFollowAuthoredSource(t *testing.T) {
	served := "# Guide\n\n## \\[Guide](acdc://docs/other)\n\nBody.\n"
	reader := newFakeReader()
	reader.snapshot.docs[guideURI] = webui.Document{
		URI: guideURI, Path: guidePath,
		Content: served,
		Source:  "# Guide\n\n## \\[Guide](other.md)\n\nBody.\n",
	}

	rec := serve(newHandler(t, &fakeBackend{}, reader), http.MethodGet, "/api/v1/document?uri="+url.QueryEscape(guideURI))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Content     string `json:"content"`
		ContentHTML string `json:"content_html"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, served, body.Content)
	require.Contains(t, body.ContentHTML, `id="guideothermd"`)
	require.NotContains(t, body.ContentHTML, `id="guideacdcdocsother"`)
}
