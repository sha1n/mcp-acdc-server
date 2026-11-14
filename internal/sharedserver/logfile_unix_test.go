//go:build unix

package sharedserver

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenLog_CreatesTheDirectory0700AndTheFile0600(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "acdc-mcp")

	file, issues := openLog(dir, testIdentity)

	require.NotNil(t, file)
	t.Cleanup(func() { _ = file.Close() })
	assert.Empty(t, issues)
	assert.Equal(t, filepath.Join(dir, testIdentity+".log"), file.Name())
	assert.True(t, filepath.IsAbs(file.Name()))
	dirInfo, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
	fileInfo, err := os.Stat(file.Name())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())
}

func TestOpenLog_AppendsToAnExistingLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, testIdentity+".log")
	require.NoError(t, os.WriteFile(path, []byte("earlier\n"), 0o600))

	file, _ := openLog(dir, testIdentity)
	require.NotNil(t, file)
	_, err := file.WriteString("later\n")
	require.NoError(t, err)
	require.NoError(t, file.Close())

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "earlier\nlater\n", string(content))
}

func TestOpenLog_RotatesAndPrunesBeforeOpening(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, testIdentity+".log")
	writeLogTestFile(t, current, 10_485_761, 0)
	staleOther := filepath.Join(dir, otherLogTestIdentity+".log")
	writeLogTestFile(t, staleOther, 1, 8*24*time.Hour)

	file, issues := openLog(dir, testIdentity)

	require.NotNil(t, file)
	t.Cleanup(func() { _ = file.Close() })
	assert.Empty(t, issues)
	info, err := os.Stat(current)
	require.NoError(t, err)
	assert.Zero(t, info.Size(), "the oversized log must have been rotated away before opening")
	assert.FileExists(t, current+".1")
	assert.NoFileExists(t, staleOther)
}

func TestOpenLog_UnopenableDirectoryYieldsNoFile(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T) (dir string, untouched string)
	}{
		{
			name: "relative directory",
			prepare: func(t *testing.T) (string, string) {
				cwd := t.TempDir()
				t.Chdir(cwd)
				return filepath.Join("state", "acdc-mcp"), cwd
			},
		},
		{
			name: "empty directory",
			prepare: func(t *testing.T) (string, string) {
				cwd := t.TempDir()
				t.Chdir(cwd)
				return "", cwd
			},
		},
		{
			name: "parent is a regular file",
			prepare: func(t *testing.T) (string, string) {
				parent := filepath.Join(t.TempDir(), "file")
				require.NoError(t, os.WriteFile(parent, nil, 0o600))
				return filepath.Join(parent, "acdc-mcp"), ""
			},
		},
		{
			name: "unwritable directory",
			prepare: func(t *testing.T) (string, string) {
				dir := filepath.Join(t.TempDir(), "readonly")
				require.NoError(t, os.Mkdir(dir, 0o500))
				t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
				return dir, dir
			},
		},
		{
			name: "log path is a symlink",
			prepare: func(t *testing.T) (string, string) {
				dir := t.TempDir()
				target := filepath.Join(t.TempDir(), "elsewhere")
				require.NoError(t, os.Symlink(target, filepath.Join(dir, testIdentity+".log")))
				return dir, filepath.Dir(target)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, untouched := tt.prepare(t)

			file, issues := openLog(dir, testIdentity)

			assert.Nil(t, file)
			assert.NotEmpty(t, issues)
			if untouched != "" {
				entries, err := os.ReadDir(untouched)
				require.NoError(t, err)
				assert.Empty(t, entries, "nothing may be created in %s", untouched)
			}
		})
	}
}

func TestStartLog_InstallsTheFileLoggerAndRestoresTheDefault(t *testing.T) {
	previous := slog.Default()
	dir := t.TempDir()

	logger, path, stop := startLog(dir, testIdentity)

	assert.Equal(t, filepath.Join(dir, testIdentity+".log"), path)
	assert.Same(t, logger, slog.Default(), "the shared server's entries must all reach its log")
	slog.Info("routed entry")
	logger.Debug("below the Info level")
	stop()
	assert.Same(t, previous, slog.Default())

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Regexp(t, `^time=\S+ level=INFO msg="routed entry"\n$`, string(content))
}

func TestStartLog_UnopenableLogDiscardsEntries(t *testing.T) {
	previous := slog.Default()
	parent := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(parent, nil, 0o600))

	logger, path, stop := startLog(filepath.Join(parent, "acdc-mcp"), testIdentity)

	assert.Empty(t, path)
	assert.Same(t, logger, slog.Default())
	assert.False(t, logger.Enabled(context.Background(), slog.LevelError), "a discarded log must not format entries")
	stop()
	assert.Same(t, previous, slog.Default())
}

func TestStartLog_MaintenanceFailuresAreWrittenAsWarnings(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, testIdentity+".log")
	writeLogTestFile(t, current, 10_485_761, 0)
	// A directory at the rotation target makes the rename fail while the
	// log itself stays openable.
	require.NoError(t, os.Mkdir(current+".1", 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(current+".1", "keep"), nil, 0o600))

	_, path, stop := startLog(dir, testIdentity)
	stop()

	require.Equal(t, current, path)
	file, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	_, err = file.Seek(10_485_761, 0)
	require.NoError(t, err)
	tail := make([]byte, 4096)
	n, _ := file.Read(tail)
	assert.Contains(t, string(tail[:n]), "level=WARN")
	assert.Contains(t, string(tail[:n]), "rotate")
}
