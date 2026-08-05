package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sha1n/mcp-acdc-server/internal/content"
)

// rejectLegacyContentLayout refuses to serve a content root still laid out for
// the superseded mcp-* convention (mcp-metadata.yaml, mcp-prompts/,
// mcp-resources/), as opposed to warnIfLegacyLayoutIgnored's unrelated "legacy"
// sense — the still-supported .acdc/resources/ frontmatter discovery mode.
// mcp-metadata.yaml and mcp-prompts/ degrade silently rather than failing: a
// stale manifest falls through to zero-config defaults with a different
// identity and a different indexed file set, and prompts vanish with no
// error. mcp-resources/ only joins the refusal when a manifest is present
// (see content.DetectLegacyLayout): zero-config discovery never scans it
// regardless, so a bare leftover directory has no silent behavior to convert
// into a visible one, and the name is too plausible in an unrelated
// repository to hard-fail on by itself.
//
// A content root that does not exist yields no markers, so resolveMetadata
// still reports the directory problem for a mistyped --content-dir.
func rejectLegacyContentLayout(cp *content.ContentProvider) error {
	found := content.DetectLegacyLayout(cp.ContentDir)
	if len(found) == 0 {
		return nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "legacy ACDC layout detected in %s\n", cp.ContentDir)
	for _, p := range found {
		fmt.Fprintf(&b, "  %-18s ->  %s\n", p.Old, p.New)
	}
	b.WriteString("Move these paths and restart.\n")
	b.WriteString("See https://github.com/sha1n/mcp-acdc-server/blob/master/docs/authoring-resources.md#migrating")

	return errors.New(b.String())
}
