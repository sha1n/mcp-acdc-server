package sharedserver

import (
	"context"
	"log/slog"
	"slices"
)

const sessionAttrKey = "session"

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

// derivation is one WithAttrs or WithGroup call, kept so it can be replayed
// on top of the session attribute.
type derivation struct {
	attrs []slog.Attr
	group string
}

func (d derivation) apply(inner slog.Handler) slog.Handler {
	if d.group != "" {
		return inner.WithGroup(d.group)
	}
	return inner.WithAttrs(d.attrs)
}

// sessionHandler adds the session carried by an entry's context as a
// top-level attribute, even under groups derived later, by replaying those
// derivations on top of it. The context is the only source of the session:
// attributes a caller supplies are never removed or rewritten, so a caller
// that adds its own session attribute gets exactly what it wrote.
type sessionHandler struct {
	base    slog.Handler
	derived []derivation
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
	attributed := h.base.WithAttrs([]slog.Attr{slog.String(sessionAttrKey, session)})
	for _, d := range h.derived {
		attributed = d.apply(attributed)
	}
	return attributed.Handle(ctx, record)
}

// WithAttrs resolves attrs once, as the wrapped handler would at derivation,
// so a replay neither invokes a LogValuer again nor sees a value it has since
// changed to.
func (h *sessionHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h.derive(derivation{attrs: resolveAttrs(attrs)})
}

func (h *sessionHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return h.derive(derivation{group: name})
}

func (h *sessionHandler) derive(d derivation) slog.Handler {
	return &sessionHandler{
		base:    h.base,
		derived: append(slices.Clip(h.derived), d),
		handler: d.apply(h.handler),
	}
}

func resolveAttrs(attrs []slog.Attr) []slog.Attr {
	resolved := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		resolved[i] = slog.Attr{Key: a.Key, Value: resolveValue(a.Value)}
	}
	return resolved
}

func resolveValue(v slog.Value) slog.Value {
	v = v.Resolve()
	if v.Kind() != slog.KindGroup {
		return v
	}
	return slog.GroupValue(resolveAttrs(v.Group())...)
}
