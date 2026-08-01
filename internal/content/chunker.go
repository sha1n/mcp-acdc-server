package content

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/yuin/goldmark/ast"
)

const maxChunkRunes = 4000

type markdownBlock struct {
	start int
	stop  int
	line  int
	end   int
	kind  ast.NodeKind
}

type markdownSection struct {
	headings []string
	base     string
	blocks   []markdownBlock
}

// ChunkMarkdown divides parsed Markdown into stable, heading-aware chunks.
func ChunkMarkdown(source domain.SourceDocument, parsed *ParsedMarkdown) ([]domain.Chunk, error) {
	if parsed == nil {
		return nil, fmt.Errorf("parsed markdown is required")
	}
	if parsed.root == nil {
		return nil, fmt.Errorf("parsed markdown AST is required")
	}

	sections := []markdownSection{{base: "document"}}
	current := 0
	usedFragments := map[string]bool{"document": true}
	fragmentCounts := map[string]int{"document": 1}
	var headingStack [6]string

	for node := parsed.root.FirstChild(); node != nil; node = node.NextSibling() {
		heading, isHeading := node.(*ast.Heading)
		if isHeading {
			level := heading.Level
			if level < 1 || level > len(headingStack) {
				continue
			}

			headingStack[level-1] = strings.TrimSpace(nodeText(heading, parsed.Body))
			for index := level; index < len(headingStack); index++ {
				headingStack[index] = ""
			}

			if headingStack[level-1] == "" {
				continue
			}

			path := headingPath(headingStack)
			section := markdownSection{
				headings: path,
				base:     allocateFragment(slugifyHeading(headingStack[level-1]), usedFragments, fragmentCounts),
			}
			if block, ok := sourceBlock(node, parsed.Body, parsed.BodyStartLine); ok {
				section.blocks = append(section.blocks, block)
			}
			sections = append(sections, section)
			current = len(sections) - 1
			continue
		}

		if block, ok := sourceBlock(node, parsed.Body, parsed.BodyStartLine); ok {
			sections[current].blocks = append(sections[current].blocks, block)
		}
	}

	var chunks []domain.Chunk
	for _, section := range sections {
		parts := splitSection(section, parsed.Body)
		for part, blocks := range parts {
			start := blocks[0].start
			stop := blocks[len(blocks)-1].stop
			fragment := section.base
			partCount := len(parts)
			if partCount > 1 {
				fragment = fmt.Sprintf("%s~%d", section.base, part+1)
			}

			chunks = append(chunks, domain.Chunk{
				ID:              source.ID + "#" + fragment,
				SourceID:        source.ID,
				SourceURI:       source.URI,
				ChunkURI:        source.URI + "#" + fragment,
				SourceTitle:     source.Name,
				SourcePath:      source.RelativePath,
				PathLabels:      append([]string(nil), source.PathLabels...),
				HeadingPath:     append([]string(nil), section.headings...),
				SectionFragment: section.base,
				Fragment:        fragment,
				Part:            part + 1,
				PartCount:       partCount,
				StartLine:       blocks[0].line,
				EndLine:         blocks[len(blocks)-1].end,
				Content:         string(parsed.Body[start:stop]),
				Keywords:        append([]string(nil), source.Keywords...),
			})
		}
	}

	return chunks, nil
}

func headingPath(stack [6]string) []string {
	path := make([]string, 0, len(stack))
	for _, heading := range stack {
		if heading != "" {
			path = append(path, heading)
		}
	}
	return path
}

func allocateFragment(base string, used map[string]bool, counts map[string]int) string {
	if base == "" {
		base = "section"
	}
	if !used[base] {
		used[base] = true
		counts[base] = 1
		return base
	}

	for suffix := counts[base]; ; suffix++ {
		candidate := fmt.Sprintf("%s-%d", base, suffix)
		if !used[candidate] {
			used[candidate] = true
			counts[base] = suffix + 1
			return candidate
		}
	}
}

func splitSection(section markdownSection, source []byte) [][]markdownBlock {
	if len(section.blocks) == 0 {
		return nil
	}

	parts := make([][]markdownBlock, 0, 1)
	current := make([]markdownBlock, 0, len(section.blocks))
	for _, block := range section.blocks {
		start := block.start
		if len(current) > 0 {
			start = current[0].start
		}
		combinedRunes := utf8.RuneCount(source[start:block.stop])
		blockRunes := utf8.RuneCount(source[block.start:block.stop])
		if len(current) > 0 && combinedRunes > maxChunkRunes {
			keepHeadingWithOversizedBlock := blockRunes > maxChunkRunes && len(current) == 1 && current[0].kind == ast.KindHeading
			if !keepHeadingWithOversizedBlock {
				parts = append(parts, current)
				current = nil
			}
		}
		current = append(current, block)
	}
	if len(current) > 0 {
		parts = append(parts, current)
	}
	return parts
}

func sourceBlock(node ast.Node, source []byte, bodyStartLine int) (markdownBlock, bool) {
	start, stop, ok := nodeSourceRange(node)
	if !ok {
		start = node.Pos()
		if start < 0 {
			return markdownBlock{}, false
		}
		stop = start
	}

	start = lineStart(source, start)
	stop = lineStop(source, stop)
	if _, fenced := node.(*ast.FencedCodeBlock); fenced {
		if !isFenceLine(source, start) {
			start = previousLineStart(source, start)
		}
		if stop < len(source) && isFenceLine(source, stop) {
			stop = lineStop(source, stop)
		}
	}
	if _, isHeading := node.(*ast.Heading); isHeading && !isATXHeadingLine(source, start) && isSetextUnderline(source, stop) {
		stop = lineStop(source, stop)
	}

	return markdownBlock{
		start: start,
		stop:  stop,
		line:  sourceLine(source, start) + bodyStartLine - 1,
		end:   sourceLine(source, stop-1) + bodyStartLine - 1,
		kind:  node.Kind(),
	}, true
}

func nodeSourceRange(node ast.Node) (int, int, bool) {
	start, stop := 0, 0
	found := false
	visit := func(segmentStart, segmentStop int) {
		if segmentStart >= segmentStop {
			return
		}
		if !found || segmentStart < start {
			start = segmentStart
		}
		if !found || segmentStop > stop {
			stop = segmentStop
		}
		found = true
	}

	_ = ast.Walk(node, func(descendant ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if descendant.Type() != ast.TypeInline {
			for index := 0; index < descendant.Lines().Len(); index++ {
				segment := descendant.Lines().At(index)
				visit(segment.Start, segment.Stop)
			}
		}
		if textNode, ok := descendant.(*ast.Text); ok {
			visit(textNode.Segment.Start, textNode.Segment.Stop)
		}
		return ast.WalkContinue, nil
	})
	return start, stop, found
}

func lineStart(source []byte, offset int) int {
	for offset > 0 && source[offset-1] != '\n' {
		offset--
	}
	return offset
}

func previousLineStart(source []byte, offset int) int {
	start := lineStart(source, offset)
	if start == 0 {
		return 0
	}
	return lineStart(source, start-1)
}

func lineStop(source []byte, offset int) int {
	for offset < len(source) && source[offset] != '\n' {
		offset++
	}
	if offset < len(source) {
		offset++
	}
	return offset
}

func sourceLine(source []byte, offset int) int {
	if offset < 0 {
		return 1
	}
	if offset > len(source) {
		offset = len(source)
	}
	return bytes.Count(source[:offset], []byte("\n")) + 1
}

func isFenceLine(source []byte, offset int) bool {
	for offset < len(source) && source[offset] == ' ' {
		offset++
	}
	if offset >= len(source) || (source[offset] != '`' && source[offset] != '~') {
		return false
	}
	fence := source[offset]
	count := 0
	for offset < len(source) && source[offset] == fence {
		count++
		offset++
	}
	return count >= 3
}

func isATXHeadingLine(source []byte, offset int) bool {
	for offset < len(source) && source[offset] == ' ' {
		offset++
	}
	return offset < len(source) && source[offset] == '#'
}

func isSetextUnderline(source []byte, offset int) bool {
	lineEnd := lineStop(source, offset)
	line := strings.TrimSpace(string(source[offset:lineEnd]))
	if line == "" || (line[0] != '=' && line[0] != '-') {
		return false
	}
	for index := 1; index < len(line); index++ {
		if line[index] != line[0] {
			return false
		}
	}
	return true
}

func slugifyHeading(value string) string {
	var slug strings.Builder
	separator := false
	for _, character := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case unicode.IsLetter(character), unicode.IsNumber(character):
			if separator && slug.Len() > 0 {
				slug.WriteByte('-')
			}
			slug.WriteRune(character)
			separator = false
		case unicode.IsSpace(character), character == '-', character == '_':
			separator = slug.Len() > 0
		}
	}
	if slug.Len() == 0 {
		return "section"
	}
	return slug.String()
}
