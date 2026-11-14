package sharedserver

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func newSessionLogTestLogger(level slog.Level) (*slog.Logger, *bytes.Buffer) {
	out := &bytes.Buffer{}
	inner := slog.NewTextHandler(out, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
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

func TestSessionHandler_LogsExactlyOneTopLevelSessionInEveryCombination(t *testing.T) {
	const session = "0a1b2c3d"
	attributed := withSession(context.Background(), session)
	tests := []struct {
		name string
		log  func(*slog.Logger)
		want string
	}{
		{
			name: "derived session only",
			log:  func(l *slog.Logger) { l.With("session", session).InfoContext(context.Background(), "probe") },
			want: `level=INFO msg=probe session=0a1b2c3d`,
		},
		{
			name: "derived and context session",
			log:  func(l *slog.Logger) { l.With("session", session).InfoContext(attributed, "probe") },
			want: `level=INFO msg=probe session=0a1b2c3d`,
		},
		{
			name: "derived session among other attributes before a group",
			log: func(l *slog.Logger) {
				l.With("component", "tools").With("session", session, "tool", "search").WithGroup("request").InfoContext(attributed, "probe", "id", 7)
			},
			want: `level=INFO msg=probe session=0a1b2c3d component=tools tool=search request.id=7`,
		},
		{
			name: "derived session under a group stays in that group",
			log: func(l *slog.Logger) {
				l.WithGroup("request").With("session", "nested").InfoContext(attributed, "probe")
			},
			want: `level=INFO msg=probe session=0a1b2c3d request.session=nested`,
		},
		{
			name: "record session and context session",
			log: func(l *slog.Logger) {
				l.With("component", "tools").InfoContext(attributed, "probe", "session", session, "id", 7)
			},
			want: `level=INFO msg=probe session=0a1b2c3d component=tools id=7`,
		},
		{
			name: "record session under a group stays in that group",
			log:  func(l *slog.Logger) { l.WithGroup("request").InfoContext(attributed, "probe", "session", "nested") },
			want: `level=INFO msg=probe session=0a1b2c3d request.session=nested`,
		},
		{
			name: "context session wins over a different derived one",
			log:  func(l *slog.Logger) { l.With("session", "ffffffff").InfoContext(attributed, "probe") },
			want: `level=INFO msg=probe session=0a1b2c3d`,
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
