package app

import (
	"context"
	"errors"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/stretchr/testify/require"
)

type fakeChunkStreamer struct {
	chunks              []domain.Chunk
	err                 error
	waitForCancellation bool
	canceled            chan<- struct{}
}

func (s *fakeChunkStreamer) StreamChunks(ctx context.Context, ch chan<- domain.Chunk) error {
	for _, chunk := range s.chunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ch <- chunk:
		}
	}
	if s.waitForCancellation {
		<-ctx.Done()
		if s.canceled != nil {
			s.canceled <- struct{}{}
		}
		if s.err != nil {
			return s.err
		}
		return ctx.Err()
	}
	return s.err
}

type fakeSearcher struct {
	indexErr   error
	drain      bool
	indexed    []domain.Chunk
	closeCalls int
}

func (s *fakeSearcher) Index(_ context.Context, chunks <-chan domain.Chunk) error {
	if s.drain {
		for chunk := range chunks {
			s.indexed = append(s.indexed, chunk)
		}
	}
	return s.indexErr
}

func (*fakeSearcher) Search(string, int) ([]search.SearchResult, error) { return nil, nil }
func (s *fakeSearcher) Close()                                          { s.closeCalls++ }

func TestIndexResources_Success(t *testing.T) {
	streamer := &fakeChunkStreamer{chunks: []domain.Chunk{{ID: "one"}}}
	indexer := &fakeSearcher{drain: true}

	err := IndexResources(context.Background(), streamer, indexer)

	require.NoError(t, err)
	require.Equal(t, []domain.Chunk{{ID: "one"}}, indexer.indexed)
}

func TestIndexResources_ReturnsProducerError(t *testing.T) {
	streamer := &fakeChunkStreamer{err: errors.New("stream failed")}
	indexer := &fakeSearcher{drain: true}

	err := IndexResources(context.Background(), streamer, indexer)

	require.ErrorContains(t, err, "stream chunks: stream failed")
}

func TestIndexResources_ReturnsIndexerError(t *testing.T) {
	streamer := &fakeChunkStreamer{chunks: []domain.Chunk{{ID: "one"}}}
	indexer := &fakeSearcher{indexErr: errors.New("index failed")}

	err := IndexResources(context.Background(), streamer, indexer)

	require.ErrorContains(t, err, "index chunks: index failed")
}

func TestIndexResources_PrefersProducerErrorWhenProducerFinishesFirst(t *testing.T) {
	streamer := &fakeChunkStreamer{err: errors.New("stream failed")}
	indexer := &fakeSearcher{drain: true, indexErr: errors.New("index failed")}

	err := IndexResources(context.Background(), streamer, indexer)

	require.ErrorContains(t, err, "stream chunks: stream failed")
}

func TestIndexResources_PrefersProducerErrorWhenIndexerFinishesFirst(t *testing.T) {
	streamer := &fakeChunkStreamer{
		err:                 errors.New("stream failed"),
		waitForCancellation: true,
	}
	indexer := &fakeSearcher{indexErr: errors.New("index failed")}

	err := IndexResources(context.Background(), streamer, indexer)

	require.ErrorContains(t, err, "stream chunks: stream failed")
}

func TestIndexResources_PrefersIndexerErrorOverProducerDeadline(t *testing.T) {
	streamer := &fakeChunkStreamer{
		err:                 context.DeadlineExceeded,
		waitForCancellation: true,
	}
	indexer := &fakeSearcher{indexErr: errors.New("index failed")}

	err := IndexResources(context.Background(), streamer, indexer)

	require.ErrorContains(t, err, "index chunks: index failed")
}

func TestIndexResources_CancelsProducerWhenIndexerFails(t *testing.T) {
	canceled := make(chan struct{}, 1)
	streamer := &fakeChunkStreamer{waitForCancellation: true, canceled: canceled}
	indexer := &fakeSearcher{indexErr: errors.New("index failed")}

	err := IndexResources(context.Background(), streamer, indexer)

	require.ErrorContains(t, err, "index chunks: index failed")
	require.Len(t, canceled, 1)
}
