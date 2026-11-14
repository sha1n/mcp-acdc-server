// Package identity computes the server identity: the name under which
// launches with equivalent effective settings, binary and startup-fixed
// content share one shared server.
package identity

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/sha1n/mcp-acdc-server/internal/config"
)

// schemaVersion must be bumped whenever canonicalisation, the exclusion list
// or the digest coverage changes, so that identities from different rules
// never collide.
const schemaVersion = 1

const identityBytes = 16

const (
	unstampedVersion = "dev"
	unstampedBuild   = "unknown"
)

// Binary describes the running executable.
type Binary struct {
	Version    string
	Build      string
	Executable string
}

func (b Binary) stamped() bool {
	return b.Version != unstampedVersion && b.Build != unstampedBuild
}

// Compute returns the server identity of s and bin as 32 lowercase hex
// characters. s must be the effective settings after loading and validation.
// An error means the identity cannot be computed.
func Compute(s *config.Settings, bin Binary) (string, error) {
	input, err := canonicalInput(s, bin)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(input)
	return hex.EncodeToString(sum[:identityBytes]), nil
}

// canonicalInput serialises every identity input as a sequence of
// length-prefixed key/value records, so no two distinct inputs share a byte
// sequence.
func canonicalInput(s *config.Settings, bin Binary) ([]byte, error) {
	contentDir, err := resolveContentDir(s.ContentDir)
	if err != nil {
		return nil, err
	}
	settings, err := settingRecords(s, contentDir)
	if err != nil {
		return nil, err
	}
	binaryRecords, err := binaryRecords(bin)
	if err != nil {
		return nil, err
	}
	content, err := readStartupFixedContent(contentDir)
	if err != nil {
		return nil, err
	}

	var w recordWriter
	w.write("schema", fmt.Sprint(schemaVersion))
	for _, r := range settings {
		w.write("setting:"+r.key, r.value)
	}
	for _, r := range binaryRecords {
		w.write("binary:"+r.key, r.value)
	}
	w.write("content:digest", string(content.digest()))
	return w.buf, nil
}

func resolveContentDir(dir string) (string, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve content directory %q: %w", dir, err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve content directory %q: %w", dir, err)
	}
	return resolved, nil
}

func binaryRecords(bin Binary) ([]record, error) {
	if bin.stamped() {
		return []record{{"version", bin.Version}, {"build", bin.Build}}, nil
	}
	sum, err := hashFile(bin.Executable)
	if err != nil {
		return nil, fmt.Errorf("hash executable of unstamped build: %w", err)
	}
	return []record{{"executable_sha256", hex.EncodeToString(sum)}}, nil
}

func hashFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return h.Sum(nil), nil
}

type record struct {
	key, value string
}

type recordWriter struct {
	buf []byte
}

func (w *recordWriter) write(key, value string) {
	w.field(key)
	w.field(value)
}

func (w *recordWriter) field(s string) {
	w.buf = binary.AppendUvarint(w.buf, uint64(len(s)))
	w.buf = append(w.buf, s...)
}
