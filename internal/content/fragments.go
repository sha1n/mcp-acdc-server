package content

import (
	"fmt"
	"strings"

	"github.com/yuin/goldmark/ast"
)

const preambleFragment = "document"

// fragmentAllocator hands out unique heading fragments. "document" is reserved
// for the preamble chunk so no heading can collide with it.
type fragmentAllocator struct {
	used   map[string]bool
	counts map[string]int
}

func newFragmentAllocator() *fragmentAllocator {
	return &fragmentAllocator{
		used:   map[string]bool{preambleFragment: true},
		counts: map[string]int{preambleFragment: 1},
	}
}

// reserve marks id as taken, so allocate never returns it and continues
// numbering duplicates of it from the first suffix.
func (a *fragmentAllocator) reserve(id string) {
	a.used[id] = true
	if a.counts[id] == 0 {
		a.counts[id] = 1
	}
}

func (a *fragmentAllocator) allocate(headingText string) string {
	base := slugifyHeading(headingText)
	if !a.used[base] {
		a.used[base] = true
		a.counts[base] = 1
		return base
	}

	for suffix := a.counts[base]; ; suffix++ {
		candidate := fmt.Sprintf("%s-%d", base, suffix)
		if !a.used[candidate] {
			a.used[candidate] = true
			a.counts[base] = suffix + 1
			return candidate
		}
	}
}

func headingLabel(heading ast.Node, source []byte) string {
	return strings.TrimSpace(nodeText(heading, source))
}

// HeadingIDs assigns an id to every heading with visible text. Top-level
// headings are allocated first, in document order, exactly as ChunkMarkdown
// allocates section fragments; nested headings follow so they never take a
// section's fragment. Headings whose only visible text is autolink labels have
// no catalog text, so they come last: ChunkMarkdown never sees them, and
// allocating them earlier would shift the fragments of the headings it does.
func HeadingIDs(doc ast.Node, source []byte) map[*ast.Heading]string {
	return HeadingIDsReserving(doc, source, nil, nil)
}

// HeadingIDsReserving is HeadingIDs restricted to the headings not in assigned,
// allocated in the same order but never returning an id in reserved.
func HeadingIDsReserving(doc ast.Node, source []byte, reserved []string, assigned map[*ast.Heading]bool) map[*ast.Heading]string {
	ids := map[*ast.Heading]string{}
	allocator := newFragmentAllocator()
	for _, id := range reserved {
		allocator.reserve(id)
	}
	var autoLinkOnly []*ast.Heading
	for _, heading := range headingsInAllocationOrder(doc) {
		if assigned[heading] {
			continue
		}
		if text := headingLabel(heading, source); text != "" {
			ids[heading] = allocator.allocate(text)
		} else {
			autoLinkOnly = append(autoLinkOnly, heading)
		}
	}
	for _, heading := range autoLinkOnly {
		if text := autoLinkLabels(heading, source); text != "" {
			ids[heading] = allocator.allocate(text)
		}
	}
	return ids
}

func headingsInAllocationOrder(doc ast.Node) []*ast.Heading {
	var topLevel, nested []*ast.Heading
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if heading, ok := node.(*ast.Heading); ok && entering {
			if heading.Parent() == doc {
				topLevel = append(topLevel, heading)
			} else {
				nested = append(nested, heading)
			}
		}
		return ast.WalkContinue, nil
	})
	return append(topLevel, nested...)
}

func autoLinkLabels(heading *ast.Heading, source []byte) string {
	var labels []string
	_ = ast.Walk(heading, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if autoLink, ok := node.(*ast.AutoLink); ok && entering {
			labels = append(labels, string(autoLink.Label(source)))
		}
		return ast.WalkContinue, nil
	})
	return strings.Join(labels, " ")
}

// HeadingAnchor maps a chunk fragment to its heading id; the preamble maps to "".
func HeadingAnchor(chunkFragment string) string {
	section, _, _ := strings.Cut(chunkFragment, "~")
	if section == preambleFragment {
		return ""
	}
	return section
}
