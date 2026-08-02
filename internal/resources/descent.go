package resources

import (
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// descentFilter bounds traversal to the directories an include pattern could
// match a file beneath, so indexing a repository costs O(selected subtrees)
// rather than O(repository).
//
// Each pattern contributes the literal directory prefix that precedes its first
// meta character. What such a prefix cannot describe -- a wildcard or brace in
// the first segment, a globstar -- degrades to a full descent, so the filter
// never hides a matchable file. A base that leaves the content root contributes
// nothing, which is equally correct: those patterns match no path relative to
// the root either.
//
// It is derived from include patterns only. An exclude subtracts from the match
// set without licensing a skipped subtree.
type descentFilter struct {
	unbounded bool
	bases     []string
}

func newDescentFilter(includes []string) descentFilter {
	var filter descentFilter
	for _, pattern := range includes {
		base, remainder := doublestar.SplitPattern(pattern)
		if base = path.Clean(base); base != "." {
			filter.bases = append(filter.bases, base)
			continue
		}
		// No literal prefix: the pattern either matches only at the root, or its
		// first segment is a wildcard that can reach any directory. Only a
		// separator or a globstar lets a remainder cross into a subdirectory.
		if strings.Contains(remainder, "/") || strings.Contains(remainder, "**") {
			filter.unbounded = true
		}
	}
	return filter
}

// allows reports whether relativeDir -- a slash path relative to the content
// root -- is inside some pattern's base, or an ancestor on the way to one.
func (filter descentFilter) allows(relativeDir string) bool {
	if filter.unbounded || relativeDir == "." {
		return true
	}
	for _, base := range filter.bases {
		if relativeDir == base ||
			strings.HasPrefix(relativeDir, base+"/") ||
			strings.HasPrefix(base, relativeDir+"/") {
			return true
		}
	}
	return false
}
