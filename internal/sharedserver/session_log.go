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
	attrs    []slog.Attr
	group    string
	topLevel bool
}

func (d derivation) apply(inner slog.Handler, attributed bool) slog.Handler {
	if d.group != "" {
		return inner.WithGroup(d.group)
	}
	if attributed && d.topLevel {
		return inner.WithAttrs(withoutSession(d.attrs))
	}
	return inner.WithAttrs(d.attrs)
}

// sessionHandler keeps the session a top-level attribute even under groups
// derived later, by replaying those derivations on top of it. The context's
// session is the connection's own, so it replaces any other top-level one.
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
		attributed = d.apply(attributed, true)
	}
	if !h.inGroup() {
		record = recordWithoutSession(record)
	}
	return attributed.Handle(ctx, record)
}

func (h *sessionHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h.derive(derivation{attrs: attrs, topLevel: !h.inGroup()})
}

func (h *sessionHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return h.derive(derivation{group: name})
}

func (h *sessionHandler) inGroup() bool {
	return slices.ContainsFunc(h.derived, func(d derivation) bool { return d.group != "" })
}

func (h *sessionHandler) derive(d derivation) slog.Handler {
	return &sessionHandler{
		base:    h.base,
		derived: append(slices.Clone(h.derived), d),
		handler: d.apply(h.handler, false),
	}
}

func withoutSession(attrs []slog.Attr) []slog.Attr {
	return slices.DeleteFunc(slices.Clone(attrs), func(a slog.Attr) bool { return a.Key == sessionAttrKey })
}

func recordWithoutSession(record slog.Record) slog.Record {
	var attrs []slog.Attr
	found := false
	record.Attrs(func(a slog.Attr) bool {
		if a.Key == sessionAttrKey {
			found = true
		} else {
			attrs = append(attrs, a)
		}
		return true
	})
	if !found {
		return record
	}
	stripped := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	stripped.AddAttrs(attrs...)
	return stripped
}
