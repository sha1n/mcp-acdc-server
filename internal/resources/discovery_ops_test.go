package resources

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestDiscoverWithOps_ConfiguredFilesystemFailures(t *testing.T) {
	root := "/catalog"
	file := root + "/docs/guide.md"
	infoErr := errors.New("entry metadata unavailable")
	canonicalErr := errors.New("file disappeared")
	containmentErr := "selected file escapes content root"
	relativeErr := errors.New("cannot make relative path")
	readErr := errors.New("cannot read selected file")
	walkErr := errors.New("directory traversal failed")

	tests := []struct {
		name      string
		configure func(ops *discoveryOps)
		wantErr   error
		contains  string
	}{
		{
			name: "walk error", wantErr: walkErr,
			configure: func(ops *discoveryOps) {
				ops.walkDir = func(path string, walk fs.WalkDirFunc) error {
					return walk(path+"/docs", nil, walkErr)
				}
			},
		},
		{
			name: "entry metadata error", wantErr: infoErr,
			configure: func(ops *discoveryOps) {
				ops.walkDir = walkOne(file, testDirEntry{name: "guide.md", infoErr: infoErr})
			},
		},
		{
			name: "canonicalization error", wantErr: canonicalErr,
			configure: func(ops *discoveryOps) {
				ops.walkDir = walkOne(file, regularEntry("guide.md"))
				ops.canonicalPath = func(path string) (string, error) {
					if path == root {
						return root, nil
					}
					return "", canonicalErr
				}
			},
		},
		{
			name: "containment error", contains: containmentErr,
			configure: func(ops *discoveryOps) {
				ops.walkDir = walkOne(file, regularEntry("guide.md"))
				ops.canonicalPath = func(path string) (string, error) {
					if path == root {
						return root, nil
					}
					return "/outside/guide.md", nil
				}
			},
		},
		{
			name: "relative path error", wantErr: relativeErr,
			configure: func(ops *discoveryOps) {
				ops.walkDir = walkOne(file, regularEntry("guide.md"))
				ops.relativePath = func(_, _ string) (string, error) { return "", relativeErr }
			},
		},
		{
			name: "directory relative path error", wantErr: relativeErr,
			configure: func(ops *discoveryOps) {
				ops.walkDir = walkOne(root+"/docs", directoryEntry("docs"))
				ops.relativePath = func(_, _ string) (string, error) { return "", relativeErr }
			},
		},
		{
			name: "read error", wantErr: readErr,
			configure: func(ops *discoveryOps) {
				ops.walkDir = walkOne(file, regularEntry("guide.md"))
				ops.readFile = func(string) ([]byte, error) { return nil, readErr }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := successfulDiscoveryOps(root)
			tt.configure(&ops)

			_, err := discoverWithOps(context.Background(), content.NewContentProvider(root), &domain.IndexMetadata{Include: []string{"docs/*.md"}}, "acdc", ops)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}
			if tt.contains != "" {
				require.ErrorContains(t, err, tt.contains)
			}
		})
	}
}

func TestDiscoverWithOps_LegacyTraversalAndCancellationFailures(t *testing.T) {
	root := "/catalog"
	resourcesRoot := root + "/mcp-resources"
	file := resourcesRoot + "/guide.md"
	walkErr := errors.New("legacy traversal failed")
	infoErr := errors.New("legacy entry metadata unavailable")
	relativeErr := errors.New("legacy relative path unavailable")

	tests := []struct {
		name      string
		configure func(ops *discoveryOps, cancel context.CancelFunc)
		wantErr   error
	}{
		{
			name: "walk error", wantErr: walkErr,
			configure: func(ops *discoveryOps, _ context.CancelFunc) {
				ops.walkDir = func(path string, walk fs.WalkDirFunc) error {
					return walk(path+"/nested", nil, walkErr)
				}
			},
		},
		{
			name: "entry metadata error", wantErr: infoErr,
			configure: func(ops *discoveryOps, _ context.CancelFunc) {
				ops.walkDir = walkOne(file, testDirEntry{name: "guide.md", infoErr: infoErr})
			},
		},
		{
			name: "relative path error", wantErr: relativeErr,
			configure: func(ops *discoveryOps, _ context.CancelFunc) {
				ops.walkDir = walkOne(file, regularEntry("guide.md"))
				ops.relativePath = func(_, _ string) (string, error) { return "", relativeErr }
			},
		},
		{
			name: "cancellation after scan", wantErr: context.Canceled,
			configure: func(ops *discoveryOps, cancel context.CancelFunc) {
				ops.walkDir = func(path string, walk fs.WalkDirFunc) error {
					require.NoError(t, walk(file, regularEntry("guide.md"), nil))
					cancel()
					return nil
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ops := successfulDiscoveryOps(root)
			tt.configure(&ops, cancel)

			_, err := discoverWithOps(ctx, content.NewContentProvider(root), nil, "acdc", ops)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestDiscoverWithOps_ConfiguredCancellationBeforeCatalogRead(t *testing.T) {
	root := "/catalog"
	file := root + "/docs/guide.md"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ops := successfulDiscoveryOps(root)
	ops.walkDir = func(path string, walk fs.WalkDirFunc) error {
		require.NoError(t, walk(file, regularEntry("guide.md"), nil))
		cancel()
		return nil
	}
	ops.readFile = func(string) ([]byte, error) {
		t.Fatal("a cancelled discovery must not read selected files")
		return nil, nil
	}

	_, err := discoverWithOps(ctx, content.NewContentProvider(root), &domain.IndexMetadata{Include: []string{"docs/*.md"}}, "acdc", ops)
	require.ErrorIs(t, err, context.Canceled)
}

func TestDiscoverWithOps_ConfiguredReadFailureLeniency(t *testing.T) {
	root := "/catalog"
	readableFile := root + "/docs/good.md"
	unreadableFile := root + "/docs/bad.md"
	readErr := errors.New("cannot read selected file")

	buildOps := func() discoveryOps {
		ops := successfulDiscoveryOps(root)
		ops.walkDir = func(_ string, walk fs.WalkDirFunc) error {
			if err := walk(unreadableFile, regularEntry("bad.md"), nil); err != nil {
				return err
			}
			return walk(readableFile, regularEntry("good.md"), nil)
		}
		ops.relativePath = func(_, path string) (string, error) {
			return strings.TrimPrefix(path, root+"/"), nil
		}
		ops.readFile = func(path string) ([]byte, error) {
			if path == unreadableFile {
				return nil, readErr
			}
			return []byte("# Good\n\nBody.\n"), nil
		}
		return ops
	}
	index := &domain.IndexMetadata{Include: []string{"docs/*.md"}}

	t.Run("lenient skips the unreadable file and indexes its readable sibling", func(t *testing.T) {
		result, err := discoverWithOps(context.Background(), content.NewContentProvider(root), index, "acdc", buildOps(), WithLenientIndex())
		require.NoError(t, err)
		require.Equal(t, []string{"acdc://docs/good"}, sourceURIs(result.Sources))
	})

	t.Run("strict fails discovery on the unreadable file", func(t *testing.T) {
		_, err := discoverWithOps(context.Background(), content.NewContentProvider(root), index, "acdc", buildOps())
		require.ErrorIs(t, err, readErr)
	})
}

func TestDiscoverWithOps_LegacySkipsUnreadableAndNonregularEntries(t *testing.T) {
	root := "/catalog"
	resourcesRoot := root + "/mcp-resources"
	file := resourcesRoot + "/guide.md"
	readErr := errors.New("legacy file unreadable")

	tests := []struct {
		name      string
		configure func(ops *discoveryOps)
	}{
		{
			name: "unreadable file",
			configure: func(ops *discoveryOps) {
				ops.walkDir = walkOne(file, regularEntry("guide.md"))
				ops.readFile = func(string) ([]byte, error) { return nil, readErr }
			},
		},
		{
			name: "nonregular file",
			configure: func(ops *discoveryOps) {
				ops.walkDir = walkOne(file, testDirEntry{name: "guide.md", mode: fs.ModeNamedPipe})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := successfulDiscoveryOps(root)
			tt.configure(&ops)

			result, err := discoverWithOps(context.Background(), content.NewContentProvider(root), nil, "acdc", ops)
			require.NoError(t, err)
			require.Empty(t, result.Sources)
			require.Empty(t, result.Chunks)
		})
	}
}

func successfulDiscoveryOps(root string) discoveryOps {
	return discoveryOps{
		canonicalPath: func(path string) (string, error) { return path, nil },
		walkDir:       walkOne(root+"/docs/guide.md", regularEntry("guide.md")),
		readFile: func(string) ([]byte, error) {
			return []byte("---\nname: Guide\ndescription: Guide description\n---\n# Guide\n"), nil
		},
		relativePath: func(root, path string) (string, error) {
			if root == "/catalog/mcp-resources" {
				return "guide.md", nil
			}
			return "docs/guide.md", nil
		},
	}
}

func walkOne(file string, entry fs.DirEntry) func(string, fs.WalkDirFunc) error {
	return func(_ string, walk fs.WalkDirFunc) error { return walk(file, entry, nil) }
}

func regularEntry(name string) testDirEntry {
	return testDirEntry{name: name}
}

func directoryEntry(name string) testDirEntry {
	return testDirEntry{name: name, mode: fs.ModeDir}
}

type testDirEntry struct {
	name    string
	infoErr error
	mode    fs.FileMode
}

func (entry testDirEntry) Name() string      { return entry.name }
func (entry testDirEntry) IsDir() bool       { return entry.mode.IsDir() }
func (entry testDirEntry) Type() fs.FileMode { return entry.mode }
func (entry testDirEntry) Info() (fs.FileInfo, error) {
	return testFileInfo{entry: entry}, entry.infoErr
}

type testFileInfo struct{ entry testDirEntry }

func (info testFileInfo) Name() string       { return info.entry.name }
func (info testFileInfo) Size() int64        { return 0 }
func (info testFileInfo) Mode() fs.FileMode  { return info.entry.mode }
func (info testFileInfo) ModTime() time.Time { return time.Time{} }
func (info testFileInfo) IsDir() bool        { return info.entry.mode.IsDir() }
func (info testFileInfo) Sys() any           { return nil }
