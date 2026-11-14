package endpoint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const testIdentity = "0123456789abcdef0123456789abcdef"

func socketPathOfLength(n int) string {
	return "/" + strings.Repeat("a", n-1)
}

func TestCheckSocketPathLength_PlatformLimits(t *testing.T) {
	tests := []struct {
		goos    string
		length  int
		wantErr bool
	}{
		{goos: "darwin", length: 103, wantErr: false},
		{goos: "darwin", length: 104, wantErr: true},
		{goos: "linux", length: 107, wantErr: false},
		{goos: "linux", length: 108, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			err := checkSocketPathLength(tt.goos, socketPathOfLength(tt.length))
			if tt.wantErr {
				assert.ErrorIs(t, err, ErrSocketPathTooLong)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidIdentity(t *testing.T) {
	tests := []struct {
		identity string
		want     bool
	}{
		{identity: testIdentity, want: true},
		{identity: strings.ToUpper(testIdentity), want: false},
		{identity: testIdentity[:31], want: false},
		{identity: testIdentity + "0", want: false},
		{identity: "../../../../etc/passwd0123456789a", want: false},
		{identity: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.identity, func(t *testing.T) {
			assert.Equal(t, tt.want, ValidIdentity(tt.identity))
		})
	}
}
