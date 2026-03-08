package app

import (
	"context"
	"log/slog"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/search"
)

// ResourceStreamer interface for things that can stream resources
type ResourceStreamer interface {
	StreamResources(ctx context.Context, ch chan<- domain.Document) error
}

// IndexResources coordinates the streaming and indexing of resources
func IndexResources(ctx context.Context, rs ResourceStreamer, indexer search.Searcher) {
	docsChan := make(chan domain.Document, 100)

	// Start producer
	go func() {
		defer close(docsChan)
		if err := rs.StreamResources(ctx, docsChan); err != nil {
			slog.Error("StreamResources failed", "error", err)
		}
	}()

	// Run consumer (blocking)
	if err := indexer.Index(ctx, docsChan); err != nil {
		slog.Error("Failed to index documents", "error", err)
	} else {
		slog.Info("Indexed documents finished")
	}
}
