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

func (m *mockResourceStreamer) StreamResources(ctx context.Context, ch chan<- domain.Document) error {
	if m.err != nil {
		return m.err
	}
	// Simulate one doc
	ch <- domain.Document{URI: "1"}
	return nil
}

type mockIndexer struct {
	err error
}

func (m *mockIndexer) Index(ctx context.Context, documents <-chan domain.Document) error {
	if m.err != nil {
		return m.err
	}
	for range documents {
		// drain
	}
	return nil
}

func (m *mockIndexer) Search(queryStr string, limit *int) ([]search.SearchResult, error) {
	return nil, nil
}
func (m *mockIndexer) Close() {}

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
