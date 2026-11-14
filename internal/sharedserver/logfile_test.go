package sharedserver

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const otherLogTestIdentity = "fedcba9876543210fedcba9876543210"

func TestResolveLogDir_PerPlatform(t *testing.T) {
	errNoHome := errors.New("no home")
	tests := []struct {
		name    string
		goos    string
		env     map[string]string
		home    string
		homeErr error
		want    string
		wantErr string
	}{
		{
			name: "linux with XDG_STATE_HOME",
			goos: "linux",
			env:  map[string]string{"XDG_STATE_HOME": "/state"},
			home: "/home/u",
			want: "/state/acdc-mcp",
		},
		{
			name: "linux with XDG_STATE_HOME unset",
			goos: "linux",
			home: "/home/u",
			want: "/home/u/.local/state/acdc-mcp",
		},
		{
			name: "linux with XDG_STATE_HOME empty",
			goos: "linux",
			env:  map[string]string{"XDG_STATE_HOME": ""},
			home: "/home/u",
			want: "/home/u/.local/state/acdc-mcp",
		},
		{
			name: "linux with a relative XDG_STATE_HOME is passed through for openLog to reject",
			goos: "linux",
			env:  map[string]string{"XDG_STATE_HOME": "state"},
			home: "/home/u",
			want: "state/acdc-mcp",
		},
		{
			name:    "linux without XDG_STATE_HOME or home",
			goos:    "linux",
			homeErr: errNoHome,
			wantErr: "failed to resolve log directory",
		},
		{
			name: "darwin",
			goos: "darwin",
			env:  map[string]string{"XDG_STATE_HOME": "/state"},
			home: "/Users/u",
			want: "/Users/u/Library/Logs/acdc-mcp",
		},
		{
			name:    "darwin without home",
			goos:    "darwin",
			homeErr: errNoHome,
			wantErr: "failed to resolve log directory",
		},
		{
			name:    "windows has no log directory",
			goos:    "windows",
			home:    `C:\Users\u`,
			wantErr: "no shared server log directory on windows",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, err := resolveLogDir(logPlatform{
				goos:        tt.goos,
				getenv:      func(key string) string { return tt.env[key] },
				userHomeDir: func() (string, error) { return tt.home, tt.homeErr },
			})

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, filepath.FromSlash(tt.want), dir)
		})
	}
}

func writeLogTestFile(t *testing.T, path string, size int64, age time.Duration) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	require.NoError(t, os.Truncate(path, size))
	modified := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(path, modified, modified))
}

func TestRotateLog_SizeThreshold(t *testing.T) {
	tests := []struct {
		name        string
		size        int64
		previous    bool
		wantRotated bool
	}{
		{name: "exactly 10 MiB stays", size: 10_485_760, wantRotated: false},
		{name: "one byte more rotates", size: 10_485_761, wantRotated: true},
		{name: "rotation replaces a previous .log.1", size: 10_485_761, previous: true, wantRotated: true},
		{name: "below the threshold keeps a previous .log.1", size: 1, previous: true, wantRotated: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			current := filepath.Join(dir, testIdentity+".log")
			rotated := current + ".1"
			writeLogTestFile(t, current, tt.size, 0)
			if tt.previous {
				writeLogTestFile(t, rotated, 3, 0)
			}

			require.NoError(t, rotateLog(dir, testIdentity))

			if tt.wantRotated {
				assert.NoFileExists(t, current)
				info, err := os.Stat(rotated)
				require.NoError(t, err)
				assert.Equal(t, tt.size, info.Size(), "the oversized log must become .log.1")
				return
			}
			info, err := os.Stat(current)
			require.NoError(t, err)
			assert.Equal(t, tt.size, info.Size())
			if tt.previous {
				info, err := os.Stat(rotated)
				require.NoError(t, err)
				assert.Equal(t, int64(3), info.Size())
			} else {
				assert.NoFileExists(t, rotated)
			}
		})
	}
}

func TestRotateLog_MissingLogIsNotAnError(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, rotateLog(dir, testIdentity))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestPruneLogs_DeletesStaleLogsAndKeepsEverythingElse(t *testing.T) {
	dir := t.TempDir()
	stale := 7*24*time.Hour + time.Hour
	fresh := 7*24*time.Hour - time.Hour
	files := []struct {
		name     string
		age      time.Duration
		wantKept bool
	}{
		{name: otherLogTestIdentity + ".log", age: stale, wantKept: false},
		{name: otherLogTestIdentity + ".log.1", age: stale, wantKept: false},
		{name: testIdentity + ".log", age: fresh, wantKept: true},
		{name: testIdentity + ".log.1", age: fresh, wantKept: true},
		{name: otherLogTestIdentity + ".log.2", age: stale, wantKept: true},
		{name: otherLogTestIdentity + ".lock", age: stale, wantKept: true},
		{name: "notes.txt", age: stale, wantKept: true},
	}
	for _, f := range files {
		writeLogTestFile(t, filepath.Join(dir, f.name), 1, f.age)
	}
	staleDir := filepath.Join(dir, "old.log")
	require.NoError(t, os.Mkdir(staleDir, 0o700))
	modified := time.Now().Add(-stale)
	require.NoError(t, os.Chtimes(staleDir, modified, modified))

	require.NoError(t, pruneLogs(dir))

	for _, f := range files {
		if f.wantKept {
			assert.FileExists(t, filepath.Join(dir, f.name))
		} else {
			assert.NoFileExists(t, filepath.Join(dir, f.name))
		}
	}
	assert.DirExists(t, staleDir, "only regular files are pruned")
}
