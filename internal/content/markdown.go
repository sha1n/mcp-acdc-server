package content

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"unicode"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"
)

var markdownParser = goldmark.New(goldmark.WithExtensions(extension.GFM))

type FrontmatterMode uint8

const (
	FrontmatterRequired FrontmatterMode = iota
	FrontmatterOptional
)

type ParsedMarkdown struct {
	Metadata      map[string]any
	Body          []byte
	BodyStartLine int
	root          ast.Node
}

type SourceOptions struct {
	URI          string
	FilePath     string
	RelativePath string
	Raw          []byte
}

func ParseMarkdown(raw []byte, mode FrontmatterMode) (*ParsedMarkdown, error) {
	normalized := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	metadata, body, bodyStartLine, err := parseFrontmatter(normalized, mode)
	if err != nil {
		return nil, err
	}

	return &ParsedMarkdown{
		Metadata:      metadata,
		Body:          body,
		BodyStartLine: bodyStartLine,
		root:          markdownParser.Parser().Parse(text.NewReader(body)),
	}, nil
}

func BuildSourceDocument(parsed *ParsedMarkdown, opts SourceOptions) (domain.SourceDocument, error) {
	if parsed == nil {
		return domain.SourceDocument{}, fmt.Errorf("parsed markdown is required")
	}

	name := metadataString(parsed.Metadata, "name")
	if name == "" {
		name = firstNodeText(parsed.root, parsed.Body, func(node ast.Node) bool {
			heading, ok := node.(*ast.Heading)
			return ok && heading.Level == 1
		})
	}

	description := metadataString(parsed.Metadata, "description")
	if description == "" {
		description = firstNodeText(parsed.root, parsed.Body, func(node ast.Node) bool {
			_, ok := node.(*ast.Paragraph)
			return ok
		})
	}
	description = truncateRunes(description, 200)

	return domain.SourceDocument{
		ID:            opts.URI,
		URI:           opts.URI,
		Name:          name,
		Description:   description,
		MIMEType:      "text/markdown",
		FilePath:      opts.FilePath,
		RelativePath:  opts.RelativePath,
		Content:       string(parsed.Body),
		Fingerprint:   fingerprint(opts.Raw),
		BodyStartLine: parsed.BodyStartLine,
		Keywords:      metadataStrings(parsed.Metadata, "keywords"),
		PathLabels:    pathLabels(opts.RelativePath),
	}, nil
}

func parseFrontmatter(raw []byte, mode FrontmatterMode) (map[string]any, []byte, int, error) {
	if !bytes.HasPrefix(raw, []byte("---\n")) {
		if mode == FrontmatterRequired {
			return nil, nil, 0, fmt.Errorf("file must start with YAML frontmatter (---\\n)")
		}
		return map[string]any{}, raw, 1, nil
	}

	const delimiterLength = len("---\n")
	remainder := raw[delimiterLength:]
	if bytes.HasPrefix(remainder, []byte("---\n")) {
		return map[string]any{}, remainder[delimiterLength:], 3, nil
	}
	if bytes.Equal(remainder, []byte("---")) {
		return map[string]any{}, nil, 3, nil
	}
	endIndex := bytes.Index(remainder, []byte("\n---"))
	if endIndex == -1 {
		return nil, nil, 0, fmt.Errorf("invalid frontmatter format - missing closing ---")
	}

	afterDelimiter := endIndex + len("\n---")
	if afterDelimiter < len(remainder) && remainder[afterDelimiter] != '\n' {
		return nil, nil, 0, fmt.Errorf("closing --- must be on its own line")
	}

	metadata := map[string]any{}
	if err := yaml.Unmarshal(remainder[:endIndex], &metadata); err != nil {
		return nil, nil, 0, fmt.Errorf("invalid YAML in frontmatter: %w", err)
	}
	if metadata == nil {
		metadata = map[string]any{}
	}

	if afterDelimiter == len(remainder) {
		return metadata, nil, bytes.Count(raw, []byte("\n")) + 1, nil
	}

	body := remainder[afterDelimiter+1:]
	bodyStartLine := bytes.Count(raw[:delimiterLength+afterDelimiter+1], []byte("\n")) + 1
	return metadata, body, bodyStartLine, nil
}

func metadataString(metadata map[string]any, key string) string {
	value, ok := metadata[key]
	if !ok {
		return ""
	}
	stringValue, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(stringValue)
}

func metadataStrings(metadata map[string]any, key string) []string {
	values, ok := metadata[key].([]any)
	if !ok {
		return nil
	}

	keywords := make([]string, 0, len(values))
	for _, value := range values {
		keyword, ok := value.(string)
		if ok {
			keywords = append(keywords, keyword)
		}
	}
	return keywords
}

func firstNodeText(root ast.Node, source []byte, matches func(ast.Node) bool) string {
	if root == nil {
		return ""
	}

	var result string
	if err := ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || !matches(node) {
			return ast.WalkContinue, nil
		}
		result = strings.TrimSpace(nodeText(node, source))
		return ast.WalkStop, nil
	}); err != nil {
		return ""
	}
	return result
}

func nodeText(node ast.Node, source []byte) string {
	var content strings.Builder
	if err := ast.Walk(node, func(descendant ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if textNode, ok := descendant.(*ast.Text); ok {
			content.Write(textNode.Segment.Value(source))
		}
		return ast.WalkContinue, nil
	}); err != nil {
		return ""
	}
	return content.String()
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func pathLabels(relativePath string) []string {
	withoutExtension := strings.TrimSuffix(relativePath, path.Ext(relativePath))
	parts := strings.FieldsFunc(withoutExtension, func(r rune) bool {
		return r == '/' || unicode.IsSpace(r) || r == '-' || r == '_'
	})

	labels := make([]string, 0, len(parts))
	seen := make(map[string]struct{})
	for _, part := range parts {
		for _, label := range splitCamelCase(part) {
			label = strings.ToLower(label)
			if _, exists := seen[label]; exists || label == "" {
				continue
			}
			seen[label] = struct{}{}
			labels = append(labels, label)
		}
	}
	return labels
}

func splitCamelCase(value string) []string {
	if value == "" {
		return nil
	}

	runes := []rune(value)
	start := 0
	parts := make([]string, 0, 1)
	for index := 1; index < len(runes); index++ {
		if unicode.IsLower(runes[index-1]) && unicode.IsUpper(runes[index]) {
			parts = append(parts, string(runes[start:index]))
			start = index
		}
	}
	parts = append(parts, string(runes[start:]))
	return parts
}

func fingerprint(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
