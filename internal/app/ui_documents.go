package app

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/sha1n/mcp-acdc-server/internal/webui"
)

type catalogDocuments struct {
	holder *resources.CatalogHolder
}

func newCatalogDocuments(holder *resources.CatalogHolder) catalogSnapshotter {
	return catalogDocuments{holder: holder}
}

// Snapshot pins one catalog generation so a document and the links resolved
// while rendering it cannot straddle a concurrent refresh.
func (d catalogDocuments) Snapshot() webui.DocumentSnapshot {
	return catalogSnapshot{provider: d.holder.Current()}
}

type catalogSnapshot struct {
	provider *resources.ResourceProvider
}

func (s catalogSnapshot) Document(uri string) (webui.Document, error) {
	source, content, ok := s.provider.SourceWithContent(uri)
	if !ok {
		return webui.Document{}, fmt.Errorf("%w: %s", webui.ErrDocumentNotFound, uri)
	}
	return webui.Document{
		URI:     source.URI,
		Title:   source.Name,
		Path:    filepath.ToSlash(source.RelativePath),
		Labels:  source.Labels(),
		Content: content,
		Source:  source.Content,
	}, nil
}

func (s catalogSnapshot) ResolvePath(fromPath, relPath string) (string, bool) {
	if path.IsAbs(relPath) {
		return "", false
	}
	resolved := path.Clean(path.Join(path.Dir(fromPath), relPath))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", false
	}
	source, ok := s.provider.SourceByPath(resolved)
	return source.URI, ok
}

func (s catalogSnapshot) ResolveURI(uri string) (string, bool) {
	source, ok := s.provider.Source(uri)
	return source.URI, ok
}
