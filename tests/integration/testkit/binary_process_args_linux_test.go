package testkit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCmdline_SplitsTheNULTerminatedArguments(t *testing.T) {
	got, err := parseCmdline([]byte("x\x00-a\x00\x00b c\x00"))

	require.NoError(t, err)
	assert.Equal(t, []string{"x", "-a", "", "b c"}, got)
}

// A zombie or a kernel thread has an empty cmdline.
func TestParseCmdline_RejectsAnEmptyCmdline(t *testing.T) {
	_, err := parseCmdline(nil)

	assert.Error(t, err)
}
