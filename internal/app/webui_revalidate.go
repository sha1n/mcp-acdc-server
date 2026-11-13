package app

import (
	"context"

	"github.com/sha1n/mcp-acdc-server/internal/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/sha1n/mcp-acdc-server/internal/webui"
)

type catalogSnapshotter interface {
	Snapshot() webui.DocumentSnapshot
}

type revalidatingSearchBackend struct {
	inner       uiSearcher
	revalidator mcp.Revalidator
}

func newRevalidatingSearchBackend(inner uiSearcher, revalidator mcp.Revalidator) webui.SearchBackend {
	return revalidatingSearchBackend{inner: inner, revalidator: revalidator}
}

func (b revalidatingSearchBackend) LabelledSearch(ctx context.Context, query string, labels []string, limit int) ([]search.LabelledResult, error) {
	b.revalidator.Revalidate(ctx)
	return b.inner.LabelledSearch(query, labels, limit)
}

func (b revalidatingSearchBackend) Labels(ctx context.Context, prefix string, limit int) ([]search.LabelCount, error) {
	b.revalidator.Revalidate(ctx)
	return b.inner.Labels(prefix, limit)
}

type revalidatingDocuments struct {
	inner       catalogSnapshotter
	revalidator mcp.Revalidator
}

func newRevalidatingDocuments(inner catalogSnapshotter, revalidator mcp.Revalidator) webui.DocumentReader {
	return revalidatingDocuments{inner: inner, revalidator: revalidator}
}

func (d revalidatingDocuments) Snapshot(ctx context.Context) webui.DocumentSnapshot {
	d.revalidator.Revalidate(ctx)
	return d.inner.Snapshot()
}
