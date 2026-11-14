//go:build unix

package sharedserver

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// formattingCause renders differently per verb, so the returned text pins the
// verb the self-check uses to format its cause.
type formattingCause struct{}

func (formattingCause) Error() string { return "plain identity error" }

func (formattingCause) Format(s fmt.State, verb rune) {
	if verb == 'v' {
		_, _ = io.WriteString(s, "formatted identity error")
		return
	}
	_, _ = io.WriteString(s, "string identity error")
}

func TestRun_SelfCheckFailureFormatsItsCauseAsBefore(t *testing.T) {
	cause := formattingCause{}
	build, _ := buildSpy(nil)

	err := Run(context.Background(), Config{
		Identity:        testIdentity,
		EndpointDir:     shortTempDir(t),
		LogDir:          t.TempDir(),
		CurrentIdentity: func() (string, error) { return "", cause },
		Build:           build,
		Linger:          time.Hour,
	})

	require.ErrorIs(t, err, ErrIdentityMismatch)
	assert.ErrorIs(t, err, cause)
	var asCause formattingCause
	assert.ErrorAs(t, err, &asCause)
	assert.Equal(t, fmt.Errorf("%w: %w", ErrIdentityMismatch, cause).Error(), err.Error())
}
