package app

import (
	"fmt"
	"strings"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/webui"
)

// catalogDocumentReader serves whole source documents to the web UI from the
// live catalog, so documents follow catalog refreshes.
type catalogDocumentReader struct {
	holder *resources.CatalogHolder
}

func newCatalogDocumentReader(holder *resources.CatalogHolder) *catalogDocumentReader {
	return &catalogDocumentReader{holder: holder}
}

func (r *catalogDocumentReader) Document(uri string) (webui.Document, error) {
	// One snapshot serves both the metadata and the body, so a concurrent
	// Swap cannot pair one catalog's title with another's content.
	catalog := r.holder.Current()
	source, ok := catalog.Source(uri)
	if !ok {
		return webui.Document{}, fmt.Errorf("%w: %s", webui.ErrDocumentNotFound, uri)
	}
	content, err := catalog.ReadResource(uri)
	if err != nil {
		return webui.Document{}, fmt.Errorf("read resource %s: %w", uri, err)
	}
	return webui.Document{
		URI:     source.URI,
		Title:   source.Name,
		Path:    source.RelativePath,
		Content: content,
		Labels:  documentLabels(source),
	}, nil
}

func documentLabels(source domain.SourceDocument) []string {
	labels := make([]string, 0, len(source.PathLabels)+len(source.Keywords))
	seen := make(map[string]struct{}, cap(labels))
	for _, group := range [][]string{source.PathLabels, source.Keywords} {
		for _, raw := range group {
			label := strings.ToLower(strings.TrimSpace(raw))
			if _, dup := seen[label]; dup || label == "" {
				continue
			}
			seen[label] = struct{}{}
			labels = append(labels, label)
		}
	}
	return labels
}
