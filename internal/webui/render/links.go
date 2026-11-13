package render

import (
	"bufio"
	"bytes"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/sha1n/mcp-acdc-server/internal/content"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
)

// LinkResolver maps an authored link target to a catalog source URI.
// Targets are fragment-free.
type LinkResolver interface {
	// ResolvePath receives a percent-decoded path relative to fromPath.
	ResolvePath(fromPath, relPath string) (uri string, ok bool)
	ResolveURI(uri string) (sourceURI string, ok bool)
}

// DocumentURL returns the web UI document page URL for sourceURI.
func DocumentURL(sourceURI, fragment string) string {
	documentURL := "/ui/doc?uri=" + url.QueryEscape(sourceURI)
	if fragment != "" {
		documentURL += "#" + fragment
	}
	return documentURL
}

// ResultDocumentURL returns the document page URL anchored at the chunk's heading.
func ResultDocumentURL(sourceURI, chunkURI string) string {
	fragment := ""
	if index := strings.LastIndexByte(chunkURI, '#'); index >= 0 {
		fragment = chunkURI[index+1:]
	}
	return DocumentURL(sourceURI, content.HeadingAnchor(fragment))
}

type linkAction int

const (
	linkFragment linkAction = iota
	linkExternal
	linkPlainText
	linkResolvePath
	linkResolveURI
	linkBroken
)

type linkPlan struct {
	action   linkAction
	target   string
	fragment string
}

// decodedDestination decodes backslash escapes and character references in a
// single pass, as CommonMark specifies, so a link is classified by the URL the
// browser will follow rather than by its spelling. goldmark's own href decoding
// resolves numeric and then named references, decoding "&#38;amp;" twice.
func decodedDestination(raw []byte) string {
	var out bytes.Buffer
	writer := bufio.NewWriter(&out)
	goldmarkhtml.DefaultWriter.Write(writer, raw)
	// Flushing into a bytes.Buffer cannot fail.
	_ = writer.Flush()
	return html.UnescapeString(out.String())
}

// undecodedDestination escapes an already decoded destination so that the
// decoding goldmark applies when rendering a link yields it unchanged.
func undecodedDestination(decoded string) []byte {
	return []byte(destinationEscaper.Replace(decoded))
}

var destinationEscaper = strings.NewReplacer(`\`, `\\`, "&", "&amp;")

var schemePattern = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.-]*):`)

func planLink(destination string) linkPlan {
	if destination == "" {
		return linkPlan{action: linkBroken}
	}
	if strings.HasPrefix(destination, "#") {
		return linkPlan{action: linkFragment, fragment: destination[1:]}
	}
	if match := schemePattern.FindStringSubmatch(destination); match != nil {
		return planSchemeLink(strings.ToLower(match[1]), destination)
	}
	return planRelativeLink(destination)
}

func planSchemeLink(scheme, destination string) linkPlan {
	switch scheme {
	case "http", "https":
		return linkPlan{action: linkExternal}
	case "mailto":
		return linkPlan{action: linkPlainText}
	}
	target, fragment, _ := strings.Cut(destination, "#")
	return linkPlan{action: linkResolveURI, target: target, fragment: fragment}
}

func planRelativeLink(destination string) linkPlan {
	if strings.HasPrefix(destination, "/") || strings.Contains(destination, "?") {
		return linkPlan{action: linkBroken}
	}
	escapedPath, fragment, _ := strings.Cut(destination, "#")
	target, err := url.PathUnescape(escapedPath)
	if err != nil {
		return linkPlan{action: linkBroken}
	}
	return linkPlan{action: linkResolvePath, target: target, fragment: fragment}
}
