package sessionproxy

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBackoff_DoublesFromInitialIntervalUpToTheCap(t *testing.T) {
	b := newBackoff()

	var got []time.Duration
	for range 8 {
		got = append(got, b.next())
	}

	assert.Equal(t, []time.Duration{
		20 * time.Millisecond,
		40 * time.Millisecond,
		80 * time.Millisecond,
		160 * time.Millisecond,
		320 * time.Millisecond,
		500 * time.Millisecond,
		500 * time.Millisecond,
		500 * time.Millisecond,
	}, got)
}
