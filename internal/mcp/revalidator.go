package mcp

import "context"

// Revalidator refreshes the served catalog before a request reads it.
type Revalidator interface {
	Revalidate(ctx context.Context)
}

// noopRevalidator is the Revalidator used when a caller does not supply one:
// requests are served from whatever catalog snapshot is current, without
// ever triggering a refresh.
type noopRevalidator struct{}

func (noopRevalidator) Revalidate(context.Context) {}
