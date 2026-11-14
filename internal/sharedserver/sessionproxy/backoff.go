package sessionproxy

import "time"

const (
	initialBackoff = 20 * time.Millisecond
	maxBackoff     = 500 * time.Millisecond
)

type clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

type backoff struct {
	interval time.Duration
}

func newBackoff() *backoff {
	return &backoff{interval: initialBackoff}
}

func (b *backoff) next() time.Duration {
	current := b.interval
	b.interval = min(b.interval*2, maxBackoff)
	return current
}
