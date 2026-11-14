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
