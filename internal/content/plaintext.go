package content

import (
	"bytes"
	"sort"
	"strings"

	"github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// PlainText is the readable text of a markdown document with its syntax
// removed, keeping enough of the source layout to locate source ranges in it.
type PlainText struct {
	Text       string
	runs       []plainTextRun
	separators []span
}

// plainTextRun maps markdown source bytes to the text they become. A verbatim
// run is copied byte for byte; a decoded run (a backslash escape or an entity
// or character reference) becomes other characters and can only be located
// whole.
type plainTextRun struct {
	source, sourceLen int
	target, targetLen int
	decoded           bool
}

// locate maps a source offset that opens or closes a located range.
func (r plainTextRun) locate(offset int, closing bool) (int, bool) {
	switch {
	case closing && offset == r.source+r.sourceLen:
		return r.target + r.targetLen, true
	case !closing && offset == r.source:
		return r.target, true
	case !r.decoded && offset > r.source && offset < r.source+r.sourceLen:
		return r.target + offset - r.source, true
	}
	return 0, false
}

// ProjectPlainText removes markdown syntax: heading markers, emphasis,
// backticks, link destinations, code fences and table pipes. Backslash
// escapes and entity and character references outside code are decoded, as a
// reader sees them. Lines within a block are joined by a space, blocks by
// " — ", table cells by " · " and table rows by " / "; separators have no
// source range, so Locate never maps into them. Raw inline HTML is kept
// verbatim as text.
func ProjectPlainText(markdown string) PlainText {
	source := []byte(markdown)
	projector := &plainTextProjector{source: source}
	root := markdownParser.Parser().Parse(text.NewReader(source))
	_ = ast.Walk(root, projector.visit)
	return PlainText{Text: projector.text.String(), runs: projector.runs, separators: projector.separators}
}

// Locate maps the markdown source byte range [start, end) onto Text. It fails
// when either end falls in syntax the projection removed or inside a decoded
// escape or reference.
func (p PlainText) Locate(start, end int) (int, int, bool) {
	from, startFound := p.target(start, false)
	to, endFound := p.target(end, true)
	return from, to, startFound && endFound && from < to
}

// target relies on runs being disjoint and in source order, so the only run
// that can locate an offset is the first one ending at or after it; an
// opening offset skips a run that ends exactly there, since it belongs to the
// run that starts there.
func (p PlainText) target(offset int, closing bool) (int, bool) {
	i := sort.Search(len(p.runs), func(i int) bool {
		end := p.runs[i].source + p.runs[i].sourceLen
		return end > offset || closing && end == offset
	})
	if i == len(p.runs) {
		return 0, false
	}
	return p.runs[i].locate(offset, closing)
}

type plainTextProjector struct {
	source     []byte
	text       strings.Builder
	runs       []plainTextRun
	separators []span
	pending    separator
}

// separator values are ordered by strength: where several boundaries meet,
// the strongest one is written, once.
type separator int

const (
	noSeparator separator = iota
	spaceSeparator
	cellSeparator
	rowSeparator
	blockSeparator
)

var separatorText = [...]string{
	spaceSeparator: " ",
	cellSeparator:  " · ",
	rowSeparator:   " / ",
	blockSeparator: " — ",
}

func blockBoundary(node ast.Node) separator {
	switch node.Kind() {
	case extast.KindTableCell:
		return cellSeparator
	case extast.KindTableRow, extast.KindTableHeader:
		return rowSeparator
	}
	return blockSeparator
}

func (p *plainTextProjector) visit(node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	if node.Type() == ast.TypeBlock {
		p.separate(blockBoundary(node))
	}
	switch n := node.(type) {
	case *ast.Text:
		p.writeText(n)
	case *ast.AutoLink:
		p.writeAutoLink(n)
	case *ast.RawHTML:
		for i := 0; i < n.Segments.Len(); i++ {
			p.writeSource(n.Segments.At(i))
		}
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		p.writeLines(n.Lines())
	case *ast.HTMLBlock:
		p.writeLines(n.Lines())
		if n.HasClosure() {
			p.writeLine(n.ClosureLine)
		}
	}
	return ast.WalkContinue, nil
}

// writeText decodes escapes and references the way goldmark's renderer does,
// except inside code spans, where markdown treats them literally.
func (p *plainTextProjector) writeText(n *ast.Text) {
	if _, code := n.Parent().(*ast.CodeSpan); code {
		p.writeSource(n.Segment)
	} else {
		p.writeDecodedText(n.Segment)
	}
	if n.SoftLineBreak() || n.HardLineBreak() {
		p.separate(spaceSeparator)
	}
}

func (p *plainTextProjector) writeDecodedText(segment text.Segment) {
	verbatimStart := segment.Start
	for i := segment.Start; i < segment.Stop; {
		decoded, width := decodeReference(p.source[i:segment.Stop])
		if width == 0 {
			i++
			continue
		}
		p.writeSource(text.NewSegment(verbatimStart, i))
		p.writeDecoded(i, width, decoded)
		i += width
		verbatimStart = i
	}
	p.writeSource(text.NewSegment(verbatimStart, segment.Stop))
}

// decodeReference decodes the backslash escape or entity or character
// reference value starts with, returning its source width, or 0 for none.
func decodeReference(value []byte) ([]byte, int) {
	if len(value) > 1 && value[0] == '\\' && util.IsPunct(value[1]) {
		return value[1:2], 2
	}
	width := referenceWidth(value)
	if width == 0 {
		return nil, 0
	}
	reference := value[:width]
	decoded := util.ResolveEntityNames(util.ResolveNumericReferences(reference))
	if bytes.Equal(decoded, reference) {
		return nil, 0
	}
	return decoded, width
}

// referenceWidth returns the width of the "&name;" or "&#digits;" shape
// value starts with, so that resolution sees exactly one reference.
func referenceWidth(value []byte) int {
	if len(value) == 0 || value[0] != '&' {
		return 0
	}
	for i := 1; i < len(value); i++ {
		switch c := value[i]; {
		case c == ';':
			return i + 1
		case c != '#' && !util.IsAlphaNumeric(c):
			return 0
		}
	}
	return 0
}

// writeAutoLink derives the label's offset from the node position, as goldmark
// exposes the label's segment only as bytes. That position is the label's
// first byte or the byte just before it: the "<" of an angle autolink, or the
// delimiter a GFM bare URL follows. The label is verbatim source text, never
// decoded, as CommonMark renders autolinks.
func (p *plainTextProjector) writeAutoLink(n *ast.AutoLink) {
	label := n.Label(p.source)
	start := n.Pos()
	if !bytes.HasPrefix(p.source[start:], label) {
		start++
	}
	p.writeSource(text.NewSegment(start, start+len(label)))
}

func (p *plainTextProjector) writeLines(lines *text.Segments) {
	for i := 0; i < lines.Len(); i++ {
		p.writeLine(lines.At(i))
	}
}

func (p *plainTextProjector) writeLine(line text.Segment) {
	p.writeSource(line.TrimRightSpace(p.source))
	p.separate(spaceSeparator)
}

// writeSource ignores the segment's padding: those are spaces goldmark
// synthesises from tab expansion, so they have no source offset.
func (p *plainTextProjector) writeSource(segment text.Segment) {
	if segment.Stop <= segment.Start {
		return
	}
	value := p.source[segment.Start:segment.Stop]
	p.addRun(plainTextRun{source: segment.Start, sourceLen: len(value), targetLen: len(value)}, value)
}

func (p *plainTextProjector) writeDecoded(start, width int, decoded []byte) {
	p.addRun(plainTextRun{source: start, sourceLen: width, targetLen: len(decoded), decoded: true}, decoded)
}

func (p *plainTextProjector) addRun(run plainTextRun, value []byte) {
	p.startWrite()
	run.target = p.text.Len()
	p.runs = append(p.runs, run)
	p.text.Write(value)
}

func (p *plainTextProjector) startWrite() {
	if p.pending != noSeparator {
		start := p.text.Len()
		p.text.WriteString(separatorText[p.pending])
		p.separators = append(p.separators, span{start, p.text.Len()})
		p.pending = noSeparator
	}
}

// separate defers the separator to the next write so the projection never
// starts or ends with one, and never repeats one around an empty block.
func (p *plainTextProjector) separate(boundary separator) {
	if p.text.Len() > 0 {
		p.pending = max(p.pending, boundary)
	}
}
