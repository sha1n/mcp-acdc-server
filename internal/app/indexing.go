package app

import (
	"context"
	"log/slog"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/search"
)

// ResourceStreamer streams chunks for indexing.
type ResourceStreamer interface {
	StreamChunks(ctx context.Context, ch chan<- domain.Chunk) error
}

// IndexResources coordinates the streaming and indexing of resources
func IndexResources(ctx context.Context, rs ResourceStreamer, indexer search.Searcher) {
	chunksChan := make(chan domain.Chunk, 100)

	// Start producer
	go func() {
		defer close(chunksChan)
		if err := rs.StreamChunks(ctx, chunksChan); err != nil {
			slog.Error("StreamChunks failed", "error", err)
		}
	}()

	// Run consumer (blocking)
	if err := indexer.Index(ctx, chunksChan); err != nil {
		slog.Error("Failed to index chunks", "error", err)
	} else {
		slog.Info("Indexed chunks finished")
	}
}
