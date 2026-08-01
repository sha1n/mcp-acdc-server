package app

import (
	"context"
	"errors"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/search"
)

type mockResourceStreamer struct {
	err error
}

func (m *mockResourceStreamer) StreamChunks(ctx context.Context, ch chan<- domain.Chunk) error {
	if m.err != nil {
		return m.err
	}
	// Simulate one chunk.
	ch <- domain.Chunk{ID: "1", SourceID: "source"}
	return nil
}

type mockIndexer struct {
	err error
}

func (m *mockIndexer) Index(ctx context.Context, chunks <-chan domain.Chunk) error {
	if m.err != nil {
		return m.err
	}
	for range chunks {
		// drain
	}
	return nil
}

func (m *mockIndexer) Search(queryStr string, candidateLimit int) ([]search.SearchResult, error) {
	return nil, nil
}
func (m *mockIndexer) Close()                                                      {}
func (m *mockIndexer) ReplaceSource(context.Context, string, []domain.Chunk) error { return nil }
func (m *mockIndexer) DeleteSource(context.Context, string) error                  { return nil }

func TestIndexResources_Success(t *testing.T) {
	rs := &mockResourceStreamer{}
	idx := &mockIndexer{}

	IndexResources(context.Background(), rs, idx)
}

func TestIndexResources_StreamError(t *testing.T) {
	rs := &mockResourceStreamer{err: errors.New("stream error")}
	idx := &mockIndexer{}

	// Should not panic, logs error
	IndexResources(context.Background(), rs, idx)
}

func TestIndexResources_IndexError(t *testing.T) {
	rs := &mockResourceStreamer{}
	idx := &mockIndexer{err: errors.New("index error")}

	// Should not panic, logs error
	IndexResources(context.Background(), rs, idx)
}
