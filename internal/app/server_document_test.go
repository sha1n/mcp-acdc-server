package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestNewSSEServer_UIServesWholeDocumentFromRealCatalogBehindAuth(t *testing.T) {
	contentDir := filepath.Join("..", "..", "examples", "sample-content")

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NoError(t, flags.Set("content-dir", contentDir))
	require.NoError(t, flags.Set("ui-enabled", "true"))
	settings, err := config.LoadSettingsWithFlags(flags)
	require.NoError(t, err)
	settings.Transport = "sse"
	settings.Search.InMemory = true
	settings.Auth = config.AuthSettings{Type: config.AuthTypeAPIKey, APIKeys: []string{"secret"}}

	mcpSrv, ui, cleanup, err := createServer(context.Background(), settings, "test", defaultFactoryDeps())
	require.NoError(t, err)
	defer cleanup()
	srv, err := NewSSEServer(mcpSrv, settings, ui.sseOptions()...)
	require.NoError(t, err)
	authed := http.Header{"X-Api-Key": []string{"secret"}}

	search := get(srv.Handler, "/api/v1/search?q=bleve", authed)
	require.Equal(t, http.StatusOK, search.Code, search.Body.String())
	var results struct {
		Results []struct {
			SourceURI string `json:"source_uri"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(search.Body.Bytes(), &results))
	require.NotEmpty(t, results.Results, search.Body.String())
	sourceURI := results.Results[0].SourceURI
	require.NotEmpty(t, sourceURI)
	documentPath := "/api/v1/document?uri=" + url.QueryEscape(sourceURI)

	require.Equal(t, http.StatusUnauthorized, get(srv.Handler, documentPath, nil).Code)

	rec := get(srv.Handler, documentPath, authed)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var doc struct {
		URI         string   `json:"uri"`
		Title       string   `json:"title"`
		Path        string   `json:"path"`
		Labels      []string `json:"labels"`
		Content     string   `json:"content"`
		ContentHTML string   `json:"content_html"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc), rec.Body.String())
	require.Equal(t, sourceURI, doc.URI)
	require.Equal(t, "reference/search-features.md", doc.Path)
	require.NotNil(t, doc.Labels, rec.Body.String())
	require.Equal(t, "Search Features", doc.Title)
	require.Contains(t, doc.Content, "## What Gets Indexed")
	require.Contains(t, doc.Content, "## Fuzzy and Prefix Matching")
	require.NotContains(t, doc.Content, "description: \"How the ACDC search engine works")
	require.Contains(t, doc.Labels, "search")
	require.Contains(t, doc.Labels, "bleve")
	require.Contains(t, doc.ContentHTML, "<h2")

	unknown := get(srv.Handler, "/api/v1/document?uri="+url.QueryEscape("acdc://does/not/exist"), authed)
	require.Equal(t, http.StatusNotFound, unknown.Code, unknown.Body.String())
}
