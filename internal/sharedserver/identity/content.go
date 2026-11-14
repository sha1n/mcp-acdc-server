package identity

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/sha1n/mcp-acdc-server/internal/content"
)

const promptExtension = ".md"

const (
	manifestRelPath = content.ConfigDirName + "/" + content.ConfigFileName
	promptsRelPath  = content.ConfigDirName + "/" + content.PromptsDirName
)

// startupFixedContent is what a server reads only once at startup and never
// reloads: the raw manifest and the raw prompt files.
type startupFixedContent struct {
	manifestPresent bool
	manifest        []byte
	prompts         []promptFile
}

type promptFile struct {
	// relPath is relative to the prompts directory, slash-separated.
	relPath string
	data    []byte
}

func readStartupFixedContent(contentDir string) (startupFixedContent, error) {
	var c startupFixedContent
	manifest, err := os.ReadFile(filepath.Join(contentDir, filepath.FromSlash(manifestRelPath)))
	switch {
	case err == nil:
		c.manifestPresent = true
		c.manifest = manifest
	case !errors.Is(err, fs.ErrNotExist):
		return startupFixedContent{}, fmt.Errorf("read manifest: %w", err)
	}

	c.prompts, err = readPrompts(filepath.Join(contentDir, filepath.FromSlash(promptsRelPath)))
	if err != nil {
		return startupFixedContent{}, err
	}
	return c, nil
}

// readPrompts selects files the way prompt discovery does: every
// non-directory entry with the exact extension ".md", symlinks not followed
// into directories. Unlike discovery, any failure is an error, because a
// skipped file would let different content share one identity.
func readPrompts(promptsDir string) ([]promptFile, error) {
	if _, err := os.Lstat(promptsDir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var prompts []promptFile
	err := filepath.WalkDir(promptsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(p) != promptExtension {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(promptsDir, p)
		if err != nil {
			return err
		}
		prompts = append(prompts, promptFile{relPath: filepath.ToSlash(rel), data: data})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk prompts directory: %w", err)
	}
	sort.Slice(prompts, func(i, j int) bool { return prompts[i].relPath < prompts[j].relPath })
	return prompts, nil
}

func (c startupFixedContent) digest() []byte {
	var w recordWriter
	if c.manifestPresent {
		w.write("manifest", string(c.manifest))
	} else {
		w.write("manifest-absent", "")
	}
	for _, p := range c.prompts {
		w.write("prompt:"+p.relPath, string(p.data))
	}
	sum := sha256.Sum256(w.buf)
	return sum[:]
}

// coveredPaths lists the content-dir-relative files that entered the digest.
func (c startupFixedContent) coveredPaths() []string {
	var paths []string
	if c.manifestPresent {
		paths = append(paths, manifestRelPath)
	}
	for _, p := range c.prompts {
		paths = append(paths, path.Join(promptsRelPath, p.relPath))
	}
	return paths
}
