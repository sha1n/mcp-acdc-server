package content

import (
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestChunkMarkdown_HeadingHierarchyAndStableFragments(t *testing.T) {
	raw := []byte("Intro.\n\n# Configuration\nParent text.\n\n## Authentication\nAPI keys.\n\n## Authentication\nTokens.\n")
	parsed, err := ParseMarkdown(raw, FrontmatterOptional)
	require.NoError(t, err)
	source, err := BuildSourceDocument(parsed, SourceOptions{URI: "acdc://docs/config", RelativePath: "docs/config.md", Raw: raw})
	require.NoError(t, err)

	chunks, err := ChunkMarkdown(source, parsed)
	require.NoError(t, err)
	require.Equal(t, []string{"document", "configuration", "authentication", "authentication-1"}, chunkFragments(chunks))
	require.Equal(t, []string{"Configuration", "Authentication"}, chunks[2].HeadingPath)
	require.Equal(t, "acdc://docs/config#authentication", chunks[2].ChunkURI)
	require.Equal(t, 6, chunks[2].StartLine)
}

func TestChunkMarkdown_SplitsOversizedSectionsAtBlockBoundaries(t *testing.T) {
	firstParagraph := strings.Repeat("a", 2100)
	secondParagraph := strings.Repeat("b", 2100)
	raw := []byte("# Large\n\n" + firstParagraph + "\n\n" + secondParagraph + "\n")
	parsed, err := ParseMarkdown(raw, FrontmatterOptional)
	require.NoError(t, err)
	source, err := BuildSourceDocument(parsed, SourceOptions{URI: "acdc://large", RelativePath: "docs/large.md", Raw: raw})
	require.NoError(t, err)

	chunks, err := ChunkMarkdown(source, parsed)
	require.NoError(t, err)
	require.Len(t, chunks, 2)
	require.Equal(t, []string{"large~1", "large~2"}, chunkFragments(chunks))
	require.Equal(t, 2, chunks[0].PartCount)
	require.NotContains(t, chunks[0].Content, chunks[1].Content)
}

func TestChunkMarkdown_KeepsIndivisibleBlocks(t *testing.T) {
	code := "```text\n" + strings.Repeat("x", 5000) + "\n```"
	raw := []byte("# Example\n\n" + code)
	parsed, err := ParseMarkdown(raw, FrontmatterOptional)
	require.NoError(t, err)
	source, err := BuildSourceDocument(parsed, SourceOptions{URI: "acdc://example", RelativePath: "docs/example.md", Raw: raw})
	require.NoError(t, err)
	chunks, err := ChunkMarkdown(source, parsed)
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Contains(t, chunks[0].Content, "```")
}

func TestChunkMarkdown_StructuralEdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		raw       []byte
		fragments []string
		assert    func(t *testing.T, chunks []domain.Chunk)
	}{
		{
			name:      "headingless document",
			raw:       []byte("First paragraph.\n\nSecond paragraph.\n"),
			fragments: []string{"document"},
			assert: func(t *testing.T, chunks []domain.Chunk) {
				require.Empty(t, chunks[0].HeadingPath)
				require.Equal(t, 1, chunks[0].StartLine)
			},
		},
		{
			name:      "empty heading with child",
			raw:       []byte("#\n\n## Child\nBody.\n"),
			fragments: []string{"child"},
			assert: func(t *testing.T, chunks []domain.Chunk) {
				require.Equal(t, []string{"Child"}, chunks[0].HeadingPath)
			},
		},
		{
			name:      "GFM table and list stay intact",
			raw:       []byte("# GFM\n\n- first\n- second\n\n| name | value |\n| --- | --- |\n| one | two |\n"),
			fragments: []string{"gfm"},
			assert: func(t *testing.T, chunks []domain.Chunk) {
				require.Contains(t, chunks[0].Content, "- first\n- second")
				require.Contains(t, chunks[0].Content, "| name | value |")
			},
		},
		{
			name:      "CRLF frontmatter adjusts source lines",
			raw:       []byte("---\r\nname: Example\r\n---\r\n# Title\r\n\r\nBody.\r\n"),
			fragments: []string{"title"},
			assert: func(t *testing.T, chunks []domain.Chunk) {
				require.Equal(t, 4, chunks[0].StartLine)
				require.Equal(t, 6, chunks[0].EndLine)
			},
		},
		{
			name:      "empty slug falls back to section",
			raw:       []byte("# !!!\nBody.\n"),
			fragments: []string{"section"},
			assert:    func(*testing.T, []domain.Chunk) {},
		},
		{
			name:      "document fragment is reserved",
			raw:       []byte("# Document\nBody.\n"),
			fragments: []string{"document-1"},
			assert:    func(*testing.T, []domain.Chunk) {},
		},
		{
			name:      "line ranges are inclusive",
			raw:       []byte("# Title\n\nFirst.\n\nSecond.\n"),
			fragments: []string{"title"},
			assert: func(t *testing.T, chunks []domain.Chunk) {
				require.Equal(t, 1, chunks[0].StartLine)
				require.Equal(t, 5, chunks[0].EndLine)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := ParseMarkdown(tt.raw, FrontmatterOptional)
			require.NoError(t, err)
			source, err := BuildSourceDocument(parsed, SourceOptions{URI: "acdc://test", RelativePath: "docs/test.md", Raw: tt.raw})
			require.NoError(t, err)

			chunks, err := ChunkMarkdown(source, parsed)
			require.NoError(t, err)
			require.Equal(t, tt.fragments, chunkFragments(chunks))
			tt.assert(t, chunks)
		})
	}
}

func TestChunkMarkdown_HeadingIDsDoNotDependOnBodyText(t *testing.T) {
	raw := []byte("# Stable\nOriginal body.\n\n## Child\nOriginal child body.\n")
	edited := []byte("# Stable\nEdited body with different words.\n\n## Child\nAlso edited.\n")

	original := chunkMarkdownForTest(t, raw)
	changed := chunkMarkdownForTest(t, edited)

	require.Equal(t, chunkFragments(original), chunkFragments(changed))
}

func chunkMarkdownForTest(t *testing.T, raw []byte) []domain.Chunk {
	t.Helper()
	parsed, err := ParseMarkdown(raw, FrontmatterOptional)
	require.NoError(t, err)
	source, err := BuildSourceDocument(parsed, SourceOptions{URI: "acdc://stable", RelativePath: "docs/stable.md", Raw: raw})
	require.NoError(t, err)
	chunks, err := ChunkMarkdown(source, parsed)
	require.NoError(t, err)
	return chunks
}

func chunkFragments(chunks []domain.Chunk) []string {
	fragments := make([]string, len(chunks))
	for index, chunk := range chunks {
		fragments[index] = chunk.Fragment
	}
	return fragments
}
