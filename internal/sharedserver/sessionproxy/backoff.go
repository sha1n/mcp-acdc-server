package sessionproxy

import "time"

const (
	initialBackoff = 20 * time.Millisecond
	maxBackoff     = 500 * time.Millisecond
)

type clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
	AfterFunc(time.Duration, func()) (stop func() bool)
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

func (realClock) AfterFunc(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }

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
