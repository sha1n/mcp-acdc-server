package integration

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrowserLocator_Locate(t *testing.T) {
	notFound := func(string) (string, error) { return "", errors.New("not found") }

	tests := []struct {
		name      string
		env       string
		onPath    map[string]string
		execs     map[string]bool
		want      string
		wantNoneF bool
		wantErr   bool
	}{
		{name: "explicit executable wins", env: "/opt/chrome", onPath: map[string]string{"chromium": "/usr/bin/chromium"},
			execs: map[string]bool{"/opt/chrome": true}, want: "/opt/chrome"},
		{name: "explicit but invalid fails instead of skipping", env: "/nonexistent",
			onPath: map[string]string{"chromium": "/usr/bin/chromium"}, wantErr: true},
		{name: "first PATH candidate in order", onPath: map[string]string{"chromium": "/usr/bin/chromium", "google-chrome-stable": "/usr/bin/gcs"},
			want: "/usr/bin/gcs"},
		{name: "macOS application bundle fallback", execs: map[string]bool{macOSChromePath: true}, want: macOSChromePath},
		{name: "nothing installed", wantNoneF: true, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			locator := browserLocator{
				getenv: func(key string) string {
					if key == browserEnvVar {
						return tc.env
					}
					return ""
				},
				lookPath: func(name string) (string, error) {
					if p, ok := tc.onPath[name]; ok {
						return p, nil
					}
					return notFound(name)
				},
				isExec: func(path string) bool { return tc.execs[path] },
			}

			got, err := locator.locate()

			if !tc.wantErr {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
				return
			}
			require.Error(t, err)
			assert.Equal(t, tc.wantNoneF, errors.Is(err, errNoBrowser))
		})
	}
}
