package app

import (
	"context"
	"errors"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/search"
	"github.com/sha1n/mcp-acdc-server/internal/webui"
	"github.com/stretchr/testify/require"
)

type revalidationContextKey struct{}

// callLog records the order in which the revalidator and the wrapped
// delegate run, so a test can prove revalidation happens before delegation,
// and the contexts the revalidator received, so a test can prove the caller's
// request context is used rather than a detached one.
type callLog struct {
	calls    []string
	contexts []context.Context
}

func (l *callLog) Revalidate(ctx context.Context) {
	l.calls = append(l.calls, "revalidate")
	l.contexts = append(l.contexts, ctx)
}

func (l *callLog) requireRequestContexts(t *testing.T, want any, count int) {
	t.Helper()
	require.Len(t, l.contexts, count)
	for _, ctx := range l.contexts {
		require.Equal(t, want, ctx.Value(revalidationContextKey{}), "Revalidate must receive the caller's context")
	}
}

// loggingUIBackend embeds fakeSearcher only to satisfy the ctx-less uiSearcher
// method set; its own LabelledSearch and Labels shadow the embedded ones.
type loggingUIBackend struct {
	*fakeSearcher
	log     *callLog
	results []search.LabelledResult
	labels  []search.LabelCount
	err     error
}

func (b loggingUIBackend) LabelledSearch(string, []string, int) ([]search.LabelledResult, error) {
	b.log.calls = append(b.log.calls, "search")
	return b.results, b.err
}

func (b loggingUIBackend) Labels(string, int) ([]search.LabelCount, error) {
	b.log.calls = append(b.log.calls, "labels")
	return b.labels, b.err
}

type loggingUIDocuments struct {
	log      *callLog
	snapshot webui.DocumentSnapshot
}

func (d loggingUIDocuments) Snapshot() webui.DocumentSnapshot {
	d.log.calls = append(d.log.calls, "snapshot")
	return d.snapshot
}

func requestContext(value string) context.Context {
	return context.WithValue(context.Background(), revalidationContextKey{}, value)
}

func TestRevalidatingSearchBackend_RevalidatesWithRequestContextBeforeDelegating(t *testing.T) {
	inner := loggingUIBackend{
		results: []search.LabelledResult{{}},
		labels:  []search.LabelCount{{Name: "ops", Count: 2}},
	}

	tests := []struct {
		name string
		call func(context.Context, webui.SearchBackend) (any, error)
		want any
		op   string
	}{
		{
			name: "search",
			call: func(ctx context.Context, b webui.SearchBackend) (any, error) {
				return b.LabelledSearch(ctx, "q", []string{"ops"}, 5)
			},
			want: inner.results,
			op:   "search",
		},
		{
			name: "labels",
			call: func(ctx context.Context, b webui.SearchBackend) (any, error) { return b.Labels(ctx, "o", 5) },
			want: inner.labels,
			op:   "labels",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := &callLog{}
			backend := inner
			backend.log = log

			got, err := tt.call(requestContext(tt.name), newRevalidatingSearchBackend(backend, log))

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.Equal(t, []string{"revalidate", tt.op}, log.calls)
			log.requireRequestContexts(t, tt.name, 1)
		})
	}
}

func TestRevalidatingSearchBackend_PropagatesDelegateError(t *testing.T) {
	log := &callLog{}
	failure := errors.New("index unavailable")
	backend := newRevalidatingSearchBackend(loggingUIBackend{log: log, err: failure}, log)
	ctx := requestContext("failing")

	_, err := backend.LabelledSearch(ctx, "q", nil, 5)
	require.ErrorIs(t, err, failure)
	_, err = backend.Labels(ctx, "", 5)
	require.ErrorIs(t, err, failure)
	require.Equal(t, []string{"revalidate", "search", "revalidate", "labels"}, log.calls)
	log.requireRequestContexts(t, "failing", 2)
}

func TestRevalidatingSearchBackend_PassesCancelledRequestContext(t *testing.T) {
	log := &callLog{}
	ctx, cancel := context.WithCancel(requestContext("cancelled"))
	cancel()

	_, err := newRevalidatingSearchBackend(loggingUIBackend{log: log}, log).LabelledSearch(ctx, "q", nil, 5)

	require.NoError(t, err)
	require.Equal(t, []string{"revalidate", "search"}, log.calls)
	require.Len(t, log.contexts, 1)
	require.ErrorIs(t, log.contexts[0].Err(), context.Canceled)
}

func TestRevalidatingDocuments_RevalidatesWithRequestContextBeforeSnapshot(t *testing.T) {
	log := &callLog{}
	snapshot := stubUISnapshot{}

	got := newRevalidatingDocuments(loggingUIDocuments{log: log, snapshot: snapshot}, log).Snapshot(requestContext("document"))

	require.Equal(t, snapshot, got)
	require.Equal(t, []string{"revalidate", "snapshot"}, log.calls)
	log.requireRequestContexts(t, "document", 1)
}
