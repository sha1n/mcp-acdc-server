package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/search"
)

// ChunkStreamer streams chunks for indexing.
type ChunkStreamer interface {
	StreamChunks(ctx context.Context, ch chan<- domain.Chunk) error
}

// IndexResources coordinates the streaming and indexing of resources.
func IndexResources(ctx context.Context, streamer ChunkStreamer, indexer search.Searcher) error {
	indexCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	chunksChan := make(chan domain.Chunk, 100)
	producerErrs := make(chan error, 1)

	go func() {
		defer close(chunksChan)
		producerErrs <- streamer.StreamChunks(indexCtx, chunksChan)
	}()

	indexErr := indexer.Index(indexCtx, chunksChan)

	var producerErr error
	select {
	case producerErr = <-producerErrs:
	default:
		cancel()
		producerErr = <-producerErrs
	}

	if producerErr != nil && (!errors.Is(producerErr, context.Canceled) || indexErr == nil) {
		return fmt.Errorf("stream chunks: %w", producerErr)
	}
	if indexErr != nil {
		return fmt.Errorf("index chunks: %w", indexErr)
	}
	return nil
}
