package resources

import (
	"path/filepath"
	"regexp"
	"strings"
)

// markdownLinkRe matches markdown links including images: ![text](target) and [text](target "title")
// It captures:
//   - Group 0 (full match): may start with '!' for images
//   - Group 1: link text
//   - Group 2: link target (URL/path part only, no title)
//   - Group 3: optional title with leading space (e.g. ` "Title"`)
var markdownLinkRe = regexp.MustCompile(`!?\[([^\]]*)\]\(([^)\s]+)(\s+"[^"]*")?\)`)

// NewCrossRefTransformer creates a ContentTransformer that rewrites relative
// markdown links to MCP resource URIs. The scheme parameter is used to
// recognize and skip links that already use the configured URI scheme.
func NewCrossRefTransformer(definitions []ResourceDefinition, scheme string) ContentTransformer {
	filePathToURI := make(map[string]string, len(definitions))
	for _, d := range definitions {
		filePathToURI[d.FilePath] = d.URI
	}

	schemePrefix := scheme + "://"

	return func(content string, currentDef ResourceDefinition) string {
		currentDir := filepath.Dir(currentDef.FilePath)

		return markdownLinkRe.ReplaceAllStringFunc(content, func(match string) string {
			// Skip image links (starting with '!')
			if strings.HasPrefix(match, "!") {
				return match
			}

			groups := markdownLinkRe.FindStringSubmatch(match)
			linkText := groups[1]
			target := groups[2]
			title := groups[3] // includes leading space, e.g. ` "Title"`

			// Skip fragment-only links
			if strings.HasPrefix(target, "#") {
				return match
			}

			// Skip links that already use the configured scheme or any other scheme
			if strings.HasPrefix(target, schemePrefix) || strings.Contains(target, "://") {
				return match
			}

			// Skip mailto: and other colon-prefixed schemes
			if strings.Contains(target, ":") {
				return match
			}

			// Separate path from fragment
			fragment := ""
			if idx := strings.Index(target, "#"); idx >= 0 {
				fragment = target[idx:]
				target = target[:idx]
			}

			// Resolve relative path against current document's directory
			resolved := filepath.Clean(filepath.Join(currentDir, target))

			// Look up in the file path to URI map
			uri, ok := filePathToURI[resolved]
			if !ok {
				return match
			}

			// Reconstruct: [text](uri#fragment "title")
			var b strings.Builder
			b.WriteString("[")
			b.WriteString(linkText)
			b.WriteString("](")
			b.WriteString(uri)
			b.WriteString(fragment)
			b.WriteString(title)
			b.WriteString(")")

			return b.String()
		})
	}
}
