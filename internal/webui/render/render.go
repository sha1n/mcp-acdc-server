// Package render converts catalog Markdown and search snippets into safe HTML for the web UI.
package render

import (
	"bufio"
	"bytes"
	"html"
	"sort"

	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Options controls how a Markdown document is rendered.
type Options struct {
	// FromPath is the catalog-relative slash path of the rendered document.
	FromPath string
	// HeadingIDs is true only for whole documents; search excerpts carry no ids.
	HeadingIDs bool
	// HeadingSource, when set with HeadingIDs, is the authored markdown the
	// rendered source was derived from with exactly the same line structure.
	// Ids are allocated over it so they match the fragments the chunker indexed:
	// a rendered heading takes the id of the authored heading that starts on the
	// same line at the same level, and any other heading gets a fresh id that no
	// authored heading holds. If the line counts differ, ids are allocated from
	// the rendered source alone.
	HeadingSource string
	// SourceURI qualifies fragment-only links when HeadingIDs is false, because
	// the target heading is not on the page that shows the excerpt.
	SourceURI string
	// References are the link reference definitions of the document an excerpt
	// was cut from, so a reference in the excerpt resolves as it would in the
	// whole document. The zero value means the excerpt's own definitions alone.
	References References
	// Links is required.
	Links LinkResolver
}

// Raw HTML is omitted because html.WithUnsafe is never set, and alignment is an
// attribute because the page CSP forbids inline styles.
var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(
		extension.WithTableCellAlignMethod(extension.TableCellAlignAttribute),
		renderer.WithNodeRenderers(util.Prioritized(placeholderRenderer{}, 100)),
	),
)

// Markdown renders source as sanitised HTML: raw HTML is dropped, images become
// placeholders and every link is rewritten to a document URL, an external link,
// plain text or a visibly broken link.
func Markdown(source string, opts Options) string {
	body := []byte(source)
	doc := markdown.Parser().Parse(text.NewReader(body), parser.WithContext(opts.References.newContext()))
	if opts.HeadingIDs {
		assignHeadingIDs(doc, body, opts.HeadingSource)
	}
	rewriteLinksAndImages(doc, body, opts)

	var out bytes.Buffer
	// Writes to a bytes.Buffer cannot fail and no node renderer in use returns an
	// error, so Render has no reachable failure path.
	_ = markdown.Renderer().Render(&out, body, doc)
	return out.String()
}

func assignHeadingIDs(doc ast.Node, body []byte, headingSource string) {
	ids := renderedHeadingIDs(doc, body, headingSource)
	for heading, id := range ids {
		heading.SetAttributeString("id", []byte(id))
	}
}

func renderedHeadingIDs(doc ast.Node, body []byte, headingSource string) map[*ast.Heading]string {
	authoredBody := []byte(headingSource)
	if headingSource == "" || bytes.Equal(authoredBody, body) || lineCount(authoredBody) != lineCount(body) {
		return content.HeadingIDs(doc, body)
	}
	return authoredHeadingIDs(doc, body, authoredBody)
}

// authoredHeadingIDs pairs headings by start line and level because the
// cross-reference transform rewrites only link destinations, which hold no
// whitespace, so line N of the rendered source is line N of the authored one.
// Block structure can still drift (a destination inside an HTML comment can
// swallow or expose its terminator), so position and level sequence prove
// nothing. Unpaired headings get fresh ids that avoid every authored id, so no
// heading takes the fragment of another section, including one the drift
// removed; that section's fragment then lands at the top of the page.
func authoredHeadingIDs(doc ast.Node, body, authoredBody []byte) map[*ast.Heading]string {
	authored := markdown.Parser().Parse(text.NewReader(authoredBody))
	authoredIDs := content.HeadingIDs(authored, authoredBody)
	authoredLines := newLineIndex(authoredBody)
	byPosition := map[headingPosition]string{}
	reserved := make([]string, 0, len(authoredIDs))
	for heading, id := range authoredIDs {
		reserved = append(reserved, id)
		if position, ok := positionOf(heading, authoredLines); ok {
			byPosition[position] = id
		}
	}

	ids := map[*ast.Heading]string{}
	paired := map[*ast.Heading]bool{}
	renderedLines := newLineIndex(body)
	for _, heading := range headingsInDocumentOrder(doc) {
		position, ok := positionOf(heading, renderedLines)
		if id, found := byPosition[position]; ok && found {
			ids[heading] = id
			paired[heading] = true
		}
	}
	for heading, id := range content.HeadingIDsReserving(doc, body, reserved, paired) {
		ids[heading] = id
	}
	return ids
}

type headingPosition struct {
	line  int
	level int
}

func positionOf(heading *ast.Heading, lines lineIndex) (headingPosition, bool) {
	if heading.Lines().Len() == 0 {
		return headingPosition{}, false
	}
	return headingPosition{line: lines.lineOf(heading.Lines().At(0).Start), level: heading.Level}, true
}

func lineCount(source []byte) int {
	return bytes.Count(source, []byte{'\n'})
}

// lineIndex holds the byte offset of every line start.
type lineIndex []int

func newLineIndex(source []byte) lineIndex {
	starts := lineIndex{0}
	for offset, b := range source {
		if b == '\n' {
			starts = append(starts, offset+1)
		}
	}
	return starts
}

func (starts lineIndex) lineOf(offset int) int {
	return sort.SearchInts(starts, offset+1) - 1
}

func headingsInDocumentOrder(doc ast.Node) []*ast.Heading {
	var headings []*ast.Heading
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if heading, ok := node.(*ast.Heading); ok && entering {
			headings = append(headings, heading)
		}
		return ast.WalkContinue, nil
	})
	return headings
}

// References holds link reference definitions collected from a document. It is
// immutable after collection and safe to share across concurrent renders.
type References struct {
	definitions []parser.Reference
}

// CollectReferences parses markdown once and keeps only its link reference
// definitions, the first definition of each label winning as in goldmark.
func CollectReferences(markdownSource string) References {
	if markdownSource == "" {
		return References{}
	}
	ctx := parser.NewContext()
	markdown.Parser().Parse(text.NewReader([]byte(markdownSource)), parser.WithContext(ctx))
	parsed := ctx.References()
	definitions := make([]parser.Reference, 0, len(parsed))
	for _, ref := range parsed {
		// goldmark's references point into the parsed AST and source buffer; owned copies let both be collected.
		definitions = append(definitions, parser.NewReference(bytes.Clone(ref.Label()), bytes.Clone(ref.Destination()), bytes.Clone(ref.Title())))
	}
	return References{definitions: definitions}
}

// newContext seeds a fresh context per render so definitions local to one
// excerpt never leak into another. Collected definitions go in first because
// goldmark keeps the first definition of a label, and an excerpt's own
// definitions also appear, at the same or a later position, in its source.
func (r References) newContext() parser.Context {
	ctx := parser.NewContext()
	for _, definition := range r.definitions {
		ctx.AddReference(definition)
	}
	return ctx
}

func rewriteLinksAndImages(doc ast.Node, source []byte, opts Options) {
	var targets []ast.Node
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node.Kind() {
		case ast.KindImage:
			targets = append(targets, node)
			return ast.WalkSkipChildren, nil
		case ast.KindLink, ast.KindAutoLink:
			targets = append(targets, node)
		}
		return ast.WalkContinue, nil
	})

	for _, node := range targets {
		switch typed := node.(type) {
		case *ast.Image:
			replaceNode(typed, &imagePlaceholder{src: decodedDestination(typed.Destination), altHTML: altHTML(typed, source)})
		case *ast.AutoLink:
			rewriteLink(autoLinkAsLink(typed, source), opts)
		case *ast.Link:
			rewriteLink(typed, opts)
		}
	}
}

func rewriteLink(link *ast.Link, opts Options) {
	destination := decodedDestination(link.Destination)
	plan := planLink(destination)
	switch plan.action {
	case linkFragment:
		destination = fragmentHref(destination, plan.fragment, opts)
	case linkExternal:
		link.SetAttributeString("target", []byte("_blank"))
		link.SetAttributeString("rel", []byte("noopener noreferrer"))
	case linkPlainText:
		unwrap(link)
		return
	default:
		href, ok := resolveHref(plan, opts)
		if !ok {
			broken := &brokenLink{target: destination}
			moveChildren(link, broken)
			replaceNode(link, broken)
			return
		}
		destination = href
	}
	link.Destination = undecodedDestination(destination)
}

func fragmentHref(destination, fragment string, opts Options) string {
	if opts.HeadingIDs || opts.SourceURI == "" {
		return destination
	}
	return DocumentURL(opts.SourceURI, fragment)
}

func resolveHref(plan linkPlan, opts Options) (string, bool) {
	var uri string
	var ok bool
	switch plan.action {
	case linkResolvePath:
		uri, ok = opts.Links.ResolvePath(opts.FromPath, plan.target)
	case linkResolveURI:
		uri, ok = opts.Links.ResolveURI(plan.target)
	}
	if !ok {
		return "", false
	}
	return DocumentURL(uri, plan.fragment), true
}

// autoLinkAsLink lets autolinks share the inline-link rules; email autolinks
// carry no scheme in their URL, so it is made explicit to classify them as mail.
func autoLinkAsLink(autoLink *ast.AutoLink, source []byte) *ast.Link {
	link := ast.NewLink()
	url := string(autoLink.URL(source))
	if autoLink.AutoLinkType == ast.AutoLinkEmail {
		url = "mailto:" + url
	}
	link.Destination = undecodedDestination(url)
	label := ast.NewString(autoLink.Label(source))
	label.SetRaw(true)
	link.AppendChild(link, label)
	replaceNode(autoLink, link)
	return link
}

func replaceNode(old, replacement ast.Node) {
	parent := old.Parent()
	parent.ReplaceChild(parent, old, replacement)
}

func moveChildren(from, to ast.Node) {
	for child := from.FirstChild(); child != nil; child = from.FirstChild() {
		to.AppendChild(to, child)
	}
}

func unwrap(node ast.Node) {
	parent := node.Parent()
	for child := node.FirstChild(); child != nil; child = node.FirstChild() {
		parent.InsertBefore(parent, node, child)
	}
	parent.RemoveChild(parent, node)
}

// altHTML mirrors goldmark's own image alt rendering, which decodes backslash
// escapes and character references in text and keeps line breaks as newlines,
// but unlike goldmark it keeps autolinks and raw HTML as visible text because
// the placeholder renders neither as markup.
func altHTML(image *ast.Image, source []byte) string {
	var out bytes.Buffer
	writer := bufio.NewWriter(&out)
	_ = ast.Walk(image, func(descendant ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch typed := descendant.(type) {
		case *ast.Text:
			writeAltText(writer, typed, source)
		case *ast.AutoLink:
			goldmarkhtml.DefaultWriter.RawWrite(writer, typed.Label(source))
		case *ast.RawHTML:
			for index := 0; index < typed.Segments.Len(); index++ {
				segment := typed.Segments.At(index)
				goldmarkhtml.DefaultWriter.RawWrite(writer, segment.Value(source))
			}
		}
		return ast.WalkContinue, nil
	})
	// Flushing into a bytes.Buffer cannot fail.
	_ = writer.Flush()
	return out.String()
}

func writeAltText(writer util.BufWriter, text *ast.Text, source []byte) {
	if text.IsRaw() {
		goldmarkhtml.DefaultWriter.RawWrite(writer, text.Segment.Value(source))
	} else {
		goldmarkhtml.DefaultWriter.Write(writer, text.Segment.Value(source))
	}
	if text.SoftLineBreak() || text.HardLineBreak() {
		_ = writer.WriteByte('\n')
	}
}

var (
	kindBrokenLink       = ast.NewNodeKind("BrokenLink")
	kindImagePlaceholder = ast.NewNodeKind("ImagePlaceholder")
)

type brokenLink struct {
	ast.BaseInline
	target string
}

func (n *brokenLink) Kind() ast.NodeKind { return kindBrokenLink }

func (n *brokenLink) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Target": n.target}, nil)
}

type imagePlaceholder struct {
	ast.BaseInline
	src     string
	altHTML string
}

func (n *imagePlaceholder) Kind() ast.NodeKind { return kindImagePlaceholder }

func (n *imagePlaceholder) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Src": n.src, "AltHTML": n.altHTML}, nil)
}

type placeholderRenderer struct{}

func (placeholderRenderer) RegisterFuncs(registerer renderer.NodeRendererFuncRegisterer) {
	registerer.Register(kindBrokenLink, renderBrokenLink)
	registerer.Register(kindImagePlaceholder, renderImagePlaceholder)
}

func renderBrokenLink(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("</span>")
		return ast.WalkContinue, nil
	}
	target := node.(*brokenLink).target
	_, _ = w.WriteString(`<span class="broken-link" title="Broken link: ` + html.EscapeString(target) + `">`)
	return ast.WalkContinue, nil
}

func renderImagePlaceholder(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	image := node.(*imagePlaceholder)
	alt := image.altHTML
	if alt == "" {
		alt = "image"
	}
	_, _ = w.WriteString(`<span class="md-image" title="Image not shown: ` + html.EscapeString(image.src) + `">` + alt + `</span>`)
	return ast.WalkSkipChildren, nil
}
