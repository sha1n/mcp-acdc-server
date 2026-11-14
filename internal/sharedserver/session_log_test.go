package sharedserver

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type syncLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func dropTime(groups []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey && len(groups) == 0 {
		return slog.Attr{}
	}
	return a
}

func newSessionLogTestLogger(level slog.Level) (*slog.Logger, *syncLogBuffer) {
	out := &syncLogBuffer{}
	inner := slog.NewTextHandler(out, &slog.HandlerOptions{Level: level, ReplaceAttr: dropTime})
	return slog.New(newSessionHandler(inner)), out
}

func TestSessionHandler_AddsTheSessionCarriedByTheContext(t *testing.T) {
	logger, out := newSessionLogTestLogger(slog.LevelInfo)

	logger.InfoContext(withSession(context.Background(), "0a1b2c3d"), "Search request", "query", "auth")

	assert.Equal(t, "level=INFO msg=\"Search request\" session=0a1b2c3d query=auth\n", out.String())
}

func TestSessionHandler_LeavesAnEntryWithoutASessionUnattributed(t *testing.T) {
	logger, out := newSessionLogTestLogger(slog.LevelInfo)

	logger.InfoContext(context.Background(), "Registered tool", "name", "search")
	logger.Info("Catalog refresh republished catalog")

	assert.Equal(t, "level=INFO msg=\"Registered tool\" name=search\nlevel=INFO msg=\"Catalog refresh republished catalog\"\n", out.String())
}

func TestSessionHandler_KeepsTheSessionTopLevelThroughWithAttrsAndWithGroup(t *testing.T) {
	logger, out := newSessionLogTestLogger(slog.LevelInfo)
	ctx := withSession(context.Background(), "0a1b2c3d")

	logger.With("component", "tools").InfoContext(ctx, "with attrs")
	logger.WithGroup("request").InfoContext(ctx, "with group", "id", 7)
	logger.With("component", "tools").InfoContext(context.Background(), "unattributed")

	assert.Equal(t, strings.Join([]string{
		`level=INFO msg="with attrs" session=0a1b2c3d component=tools`,
		`level=INFO msg="with group" session=0a1b2c3d request.id=7`,
		`level=INFO msg=unattributed component=tools`,
		"",
	}, "\n"), out.String())
}

func TestSessionHandler_DelegatesEnabledToTheWrappedHandler(t *testing.T) {
	logger, out := newSessionLogTestLogger(slog.LevelWarn)
	ctx := withSession(context.Background(), "0a1b2c3d")

	assert.False(t, logger.Enabled(ctx, slog.LevelInfo))
	assert.True(t, logger.Enabled(ctx, slog.LevelWarn))
	assert.False(t, logger.With("k", "v").Enabled(ctx, slog.LevelInfo))
	assert.False(t, logger.WithGroup("g").Enabled(ctx, slog.LevelInfo))
	logger.InfoContext(ctx, "dropped")
	logger.WarnContext(ctx, "kept")

	assert.Equal(t, "level=WARN msg=kept session=0a1b2c3d\n", out.String())
}

func TestWithSession_IgnoresAnEmptyIdentifier(t *testing.T) {
	logger, out := newSessionLogTestLogger(slog.LevelInfo)

	logger.InfoContext(withSession(context.Background(), ""), "no session")

	assert.Equal(t, "level=INFO msg=\"no session\"\n", out.String())
}

func TestSessionHandler_AddsExactlyOneTopLevelSessionThroughAnyDerivation(t *testing.T) {
	logger, out := newSessionLogTestLogger(slog.LevelInfo)
	ctx := withSession(context.Background(), "0a1b2c3d")

	logger.With("component", "tools").WithGroup("request").With("tool", "search").WithGroup("args").InfoContext(ctx, "probe", "id", 7)
	logger.WithGroup("").With("component", "tools").InfoContext(ctx, "empty group")

	assert.Equal(t, strings.Join([]string{
		`level=INFO msg=probe session=0a1b2c3d component=tools request.tool=search request.args.id=7`,
		`level=INFO msg="empty group" session=0a1b2c3d component=tools`,
		"",
	}, "\n"), out.String())
}

type sessionValuer struct{}

func (sessionValuer) LogValue() slog.Value {
	return slog.GroupValue(slog.String("session", "caller"), slog.String("keep", "yes"))
}

func TestSessionHandler_NeverRemovesOrRewritesCallerAttributes(t *testing.T) {
	ctx := withSession(context.Background(), "0a1b2c3d")
	tests := []struct {
		name string
		log  func(*slog.Logger)
		want string
	}{
		{
			name: "derived scalar session",
			log:  func(l *slog.Logger) { l.With("session", "caller").InfoContext(ctx, "probe") },
			want: `level=INFO msg=probe session=0a1b2c3d session=caller`,
		},
		{
			name: "record scalar session",
			log:  func(l *slog.Logger) { l.InfoContext(ctx, "probe", "session", "caller", "id", 7) },
			want: `level=INFO msg=probe session=0a1b2c3d session=caller id=7`,
		},
		{
			name: "named group session keeps its contents",
			log: func(l *slog.Logger) {
				l.With(slog.Group("session", slog.String("keep", "yes"))).InfoContext(ctx, "probe", slog.Group("session", slog.String("also", "kept")))
			},
			want: `level=INFO msg=probe session=0a1b2c3d session.keep=yes session.also=kept`,
		},
		{
			name: "inline group",
			log: func(l *slog.Logger) {
				l.InfoContext(ctx, "probe", slog.Group("", slog.String("session", "caller"), slog.String("keep", "yes")))
			},
			want: `level=INFO msg=probe session=0a1b2c3d session=caller keep=yes`,
		},
		{
			name: "derived inline group from a LogValuer",
			log:  func(l *slog.Logger) { l.With(slog.Any("", sessionValuer{})).InfoContext(ctx, "probe") },
			want: `level=INFO msg=probe session=0a1b2c3d session=caller keep=yes`,
		},
		{
			name: "session under a caller group",
			log: func(l *slog.Logger) {
				l.WithGroup("request").With("session", "nested").InfoContext(ctx, "probe", "session", "record")
			},
			want: `level=INFO msg=probe session=0a1b2c3d request.session=nested request.session=record`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, out := newSessionLogTestLogger(slog.LevelInfo)

			tt.log(logger)

			assert.Equal(t, tt.want+"\n", out.String())
		})
	}
}

func TestSessionHandler_WithoutASessionIsByteIdenticalToTheWrappedHandler(t *testing.T) {
	logAll := func(l *slog.Logger) {
		ctx := context.Background()
		l.With("session", "caller").InfoContext(ctx, "scalar", "session", "record")
		l.With(slog.Group("session", slog.String("keep", "yes"))).InfoContext(ctx, "named group")
		l.With(slog.Any("", sessionValuer{})).WithGroup("request").InfoContext(ctx, "valuer", slog.Group("", slog.String("session", "inline")))
		l.WithGroup("").WithGroup("request").With("id", 7).InfoContext(ctx, "groups", "session", "nested")
	}
	wrapped, got := newSessionLogTestLogger(slog.LevelInfo)
	logAll(wrapped)
	want := &bytes.Buffer{}
	logAll(slog.New(slog.NewTextHandler(want, &slog.HandlerOptions{ReplaceAttr: dropTime})))

	assert.Equal(t, want.String(), got.String())
}

type switchingValuer struct{ value *string }

func (v switchingValuer) LogValue() slog.Value { return slog.StringValue(*v.value) }

func TestSessionHandler_ReplaysTheValuesResolvedWhenAttributesWereDerived(t *testing.T) {
	logger, out := newSessionLogTestLogger(slog.LevelInfo)
	source := "original"
	derived := logger.With("value", switchingValuer{&source}).WithGroup("request").With("nested", slog.GroupValue(slog.Any("value", switchingValuer{&source})))
	source = "changed"

	derived.InfoContext(context.Background(), "unattributed")
	derived.InfoContext(withSession(context.Background(), "0a1b2c3d"), "attributed")

	assert.Equal(t, strings.Join([]string{
		`level=INFO msg=unattributed value=original request.nested.value=original`,
		`level=INFO msg=attributed session=0a1b2c3d value=original request.nested.value=original`,
		"",
	}, "\n"), out.String())
}

type countingValuer struct{ calls *atomic.Int32 }

func (v countingValuer) LogValue() slog.Value {
	v.calls.Add(1)
	return slog.GroupValue(slog.Any("inner", countingInner(v)))
}

type countingInner struct{ calls *atomic.Int32 }

func (v countingInner) LogValue() slog.Value {
	v.calls.Add(1)
	return slog.StringValue("resolved")
}

func TestSessionHandler_ResolvesADerivedLogValuerOnce(t *testing.T) {
	logger, out := newSessionLogTestLogger(slog.LevelInfo)
	calls := &atomic.Int32{}
	derived := logger.With("counted", countingValuer{calls})
	ctx := withSession(context.Background(), "0a1b2c3d")

	for range 3 {
		derived.InfoContext(ctx, "attributed")
		derived.InfoContext(context.Background(), "unattributed")
	}

	assert.Equal(t, int32(2), calls.Load(), "one call for the valuer and one for the valuer nested in its group")
	assert.Equal(t, 6, strings.Count(out.String(), "counted.inner=resolved"), out.String())
}

func TestSessionHandler_SiblingDerivationsAreIndependentUnderConcurrency(t *testing.T) {
	logger, out := newSessionLogTestLogger(slog.LevelInfo)
	parent := logger.With("a", 1).With("b", 2).With("c", 3)
	ctx := withSession(context.Background(), "0a1b2c3d")
	const siblings = 32

	var wg sync.WaitGroup
	for i := range siblings {
		wg.Go(func() {
			parent.With("sibling", i).WithGroup("g").With("own", i).InfoContext(ctx, "probe")
		})
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	require.Len(t, lines, siblings)
	seen := map[string]bool{}
	for _, line := range lines {
		var i int
		_, err := fmt.Sscanf(line, "level=INFO msg=probe session=0a1b2c3d a=1 b=2 c=3 sibling=%d", &i)
		require.NoError(t, err, line)
		assert.Equal(t, fmt.Sprintf("level=INFO msg=probe session=0a1b2c3d a=1 b=2 c=3 sibling=%d g.own=%d", i, i), line)
		seen[line] = true
	}
	assert.Len(t, seen, siblings)
}
