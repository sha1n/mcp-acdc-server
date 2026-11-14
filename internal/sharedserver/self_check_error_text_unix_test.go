//go:build unix

package sharedserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_SelfCheckFailureReturnsAStableErrorText(t *testing.T) {
	errCompute := errors.New("manifest unreadable")
	cases := map[string]struct {
		current func() (string, error)
		want    string
		cause   error
	}{
		"mismatch": {
			current: func() (string, error) { return otherTestIdentity, nil },
			want:    "shared server identity does not match its content: content identity is " + otherTestIdentity,
		},
		"uncomputable": {
			current: func() (string, error) { return "", errCompute },
			want:    "shared server identity does not match its content: manifest unreadable",
			cause:   errCompute,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			build, _ := buildSpy(nil)

			err := Run(context.Background(), Config{
				Identity:        testIdentity,
				EndpointDir:     shortTempDir(t),
				LogDir:          t.TempDir(),
				CurrentIdentity: tc.current,
				Build:           build,
				Linger:          time.Hour,
			})

			require.ErrorIs(t, err, ErrIdentityMismatch)
			assert.Equal(t, tc.want, err.Error())
			if tc.cause != nil {
				assert.ErrorIs(t, err, tc.cause, "the computation error stays in the chain")
			}
		})
	}
}
