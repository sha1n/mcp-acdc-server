package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
)

// Fingerprint digests the file set a Discover call with the same arguments
// would select: the relative path, size and modification time of each file. It
// reads no file contents, so it is a cheap gate in front of a rediscovery
// rather than a content comparison — a touched file with unchanged content
// changes the digest.
func Fingerprint(ctx context.Context, cp *content.ContentProvider, index *domain.IndexMetadata, opts ...DiscoverOption) (string, error) {
	ops := defaultDiscoveryOps()

	var files []selectedFile
	if index == nil {
		_, selected, err := selectLegacyFiles(ctx, cp, ops)
		if err != nil {
			return "", err
		}
		files = selected
	} else {
		policy := resolvePolicy(opts)
		_, selected, err := selectConfiguredFiles(ctx, cp, index, ops, policy)
		if err != nil {
			return "", err
		}
		files = selected
	}

	hasher := sha256.New()
	for _, file := range files {
		// hash.Hash.Write never returns an error (per the io.Writer contract it
		// documents), so the write's result is deliberately discarded here.
		//
		// The trailing newline is a per-file record separator, so this format
		// assumes relativePath contains no literal newline; a path with one
		// (legal on Linux and macOS) would let its record boundary land in an
		// unexpected place relative to the fields that follow it.
		_, _ = fmt.Fprintf(hasher, "%s\x00%d\x00%d\n", file.relativePath, file.size, file.modTime.UnixNano())
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
