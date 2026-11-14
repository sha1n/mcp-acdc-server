package sharedserver

import (
	"context"
	"log/slog"
	"slices"
)

type sessionKey struct{}

// withSession marks ctx as belonging to one attached session. Request
// handlers receive contexts derived from the one passed to Server.Connect,
// so whatever they log with their context is attributed to that session.
func withSession(ctx context.Context, session string) context.Context {
	if session == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionKey{}, session)
}

// sessionHandler keeps the session a top-level attribute even under groups
// derived later, by replaying those derivations on top of it.
type sessionHandler struct {
	base    slog.Handler
	derived []func(slog.Handler) slog.Handler
	handler slog.Handler
}

func newSessionHandler(inner slog.Handler) slog.Handler {
	return &sessionHandler{base: inner, handler: inner}
}

func (h *sessionHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *sessionHandler) Handle(ctx context.Context, record slog.Record) error {
	session, ok := ctx.Value(sessionKey{}).(string)
	if !ok {
		return h.handler.Handle(ctx, record)
	}
	attributed := h.base.WithAttrs([]slog.Attr{slog.String("session", session)})
	for _, derive := range h.derived {
		attributed = derive(attributed)
	}
	return attributed.Handle(ctx, record)
}

func (h *sessionHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h.derive(func(inner slog.Handler) slog.Handler { return inner.WithAttrs(attrs) })
}

func (h *sessionHandler) WithGroup(name string) slog.Handler {
	return h.derive(func(inner slog.Handler) slog.Handler { return inner.WithGroup(name) })
}

func (h *sessionHandler) derive(derivation func(slog.Handler) slog.Handler) slog.Handler {
	return &sessionHandler{
		base:    h.base,
		derived: append(slices.Clone(h.derived), derivation),
		handler: derivation(h.handler),
	}
}
