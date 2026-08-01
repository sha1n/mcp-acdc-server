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

func TestChunkMarkdown_SplitsBeforeOversizedBlockAfterPriorBody(t *testing.T) {
	raw := []byte("# Example\n\nShort paragraph.\n\n```text\n" + strings.Repeat("x", 5000) + "\n```\n")

	chunks := chunkMarkdownForTest(t, raw)

	require.Len(t, chunks, 2)
	require.Equal(t, []string{"example~1", "example~2"}, chunkFragments(chunks))
	require.Contains(t, chunks[0].Content, "Short paragraph.")
	require.NotContains(t, chunks[0].Content, "```")
	require.Contains(t, chunks[1].Content, "```")
}

func TestChunkMarkdown_PreservesBlocksWithoutDescendantSegments(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		want string
	}{
		{
			name: "trailing thematic break",
			raw:  []byte("# Heading\n\n---\n"),
			want: "---",
		},
		{
			name: "empty fenced block",
			raw:  []byte("# Heading\n\n```\n```\n"),
			want: "```\n```",
		},
		{
			name: "setext underline",
			raw:  []byte("Heading\n=======\n\nBody.\n"),
			want: "=======",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := chunkMarkdownForTest(t, tt.raw)

			require.Len(t, chunks, 1)
			require.Contains(t, chunks[0].Content, tt.want)
		})
	}
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
	require.Equal(t, chunkIDs(original), chunkIDs(changed))
	require.Equal(t, chunkURIs(original), chunkURIs(changed))
}

func TestChunkMarkdown_PopulatesCompleteChunkContract(t *testing.T) {
	raw := []byte("# Guide\n\nBody.\n")
	parsed, err := ParseMarkdown(raw, FrontmatterOptional)
	require.NoError(t, err)
	source := domain.SourceDocument{
		ID:           "source-1",
		URI:          "acdc://guide",
		Name:         "Guide title",
		RelativePath: "docs/guide.md",
		PathLabels:   []string{"docs", "guide"},
		Keywords:     []string{"alpha", "beta"},
	}

	chunks, err := ChunkMarkdown(source, parsed)
	require.NoError(t, err)
	require.Equal(t, []domain.Chunk{{
		ID:              "source-1#guide",
		SourceID:        "source-1",
		SourceURI:       "acdc://guide",
		ChunkURI:        "acdc://guide#guide",
		SourceTitle:     "Guide title",
		SourcePath:      "docs/guide.md",
		PathLabels:      []string{"docs", "guide"},
		HeadingPath:     []string{"Guide"},
		SectionFragment: "guide",
		Fragment:        "guide",
		Part:            1,
		PartCount:       1,
		StartLine:       1,
		EndLine:         3,
		Content:         "# Guide\n\nBody.\n",
		Keywords:        []string{"alpha", "beta"},
	}}, chunks)
}

func TestChunkMarkdown_OmitsEmptyLogicalSections(t *testing.T) {
	raw := []byte("# Parent\n## Child\nBody.\n")
	chunks := chunkMarkdownForTest(t, raw)

	require.Equal(t, []string{"child"}, chunkFragments(chunks))
	require.Equal(t, []string{"Parent", "Child"}, chunks[0].HeadingPath)
	require.Equal(t, "## Child\nBody.\n", chunks[0].Content)
}

func TestChunkMarkdown_EmptyDocumentProducesNoChunks(t *testing.T) {
	chunks := chunkMarkdownForTest(t, nil)

	require.Empty(t, chunks)
}

func TestChunkMarkdown_GreedilySplitsExactSourceRanges(t *testing.T) {
	first := strings.Repeat("a", 1500)
	second := strings.Repeat("b", 1500)
	third := strings.Repeat("c", 1500)
	raw := []byte("# Large\n\n" + first + "\n\n" + second + "\n\n" + third + "\n")

	chunks := chunkMarkdownForTest(t, raw)

	require.Len(t, chunks, 2)
	require.Equal(t, "# Large\n\n"+first+"\n\n"+second+"\n", chunks[0].Content)
	require.Equal(t, third+"\n", chunks[1].Content)
	require.Equal(t, []string{"large~1", "large~2"}, chunkFragments(chunks))
	require.Equal(t, 1, chunks[0].Part)
	require.Equal(t, 2, chunks[1].Part)
	require.Equal(t, 2, chunks[0].PartCount)
	require.Equal(t, 2, chunks[1].PartCount)
	require.Equal(t, 1, chunks[0].StartLine)
	require.Equal(t, 5, chunks[0].EndLine)
	require.Equal(t, 7, chunks[1].StartLine)
	require.Equal(t, 7, chunks[1].EndLine)
}

func TestChunkMarkdown_DocumentChunksUseSourceTitleOutsideHeadingPath(t *testing.T) {
	raw := []byte("Body.\n")
	parsed, err := ParseMarkdown(raw, FrontmatterOptional)
	require.NoError(t, err)
	source := domain.SourceDocument{ID: "source", URI: "acdc://guide", Name: "Guide"}

	chunks, err := ChunkMarkdown(source, parsed)
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Empty(t, chunks[0].HeadingPath)
	require.Equal(t, "Guide", chunks[0].SourceTitle)
}

func TestChunkMarkdown_NormalizesStablePublicFragments(t *testing.T) {
	raw := []byte("# API_Key\nOne.\n\n# *Résumé* Guide\nTwo.\n\n# API--Key\nThree.\n\n# API\nFour.\n\n# API-1\nFive.\n\n# API\nSix.\n")

	chunks := chunkMarkdownForTest(t, raw)

	wantFragments := []string{"api_key", "résumé-guide", "api--key", "api", "api-1", "api-2"}
	require.Equal(t, wantFragments, chunkFragments(chunks))
	for index, fragment := range wantFragments {
		require.Equal(t, "acdc://stable#"+fragment, chunks[index].ID)
		require.Equal(t, "acdc://stable#"+fragment, chunks[index].ChunkURI)
	}
}

func TestChunkMarkdown_UsesUnicodeCodePointSoftLimit(t *testing.T) {
	first := strings.Repeat("é", 1999)
	exactSecond := strings.Repeat("界", 1998)
	aboveSecond := strings.Repeat("界", 1999)
	exactRaw := []byte(first + "\n\n" + exactSecond + "\n")
	aboveRaw := []byte(first + "\n\n" + aboveSecond + "\n")

	exact := chunkMarkdownForTest(t, exactRaw)
	require.Len(t, exact, 1)
	require.Equal(t, "document", exact[0].Fragment)
	require.Equal(t, string(exactRaw), exact[0].Content)
	require.Equal(t, 1, exact[0].PartCount)

	above := chunkMarkdownForTest(t, aboveRaw)
	require.Len(t, above, 2)
	require.Equal(t, []string{"document~1", "document~2"}, chunkFragments(above))
	require.Equal(t, first+"\n", above[0].Content)
	require.Equal(t, aboveSecond+"\n", above[1].Content)
	require.Equal(t, 2, above[0].PartCount)
	require.Equal(t, 2, above[1].PartCount)
}

func TestChunkMarkdown_KeepsEveryOversizedStructuralBlockIntact(t *testing.T) {
	long := strings.Repeat("x", 4100)
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "GFM table", raw: []byte("# Table\n\n| value |\n| --- |\n| " + long + " |\n")},
		{name: "list", raw: []byte("# List\n\n- " + long + "\n")},
		{name: "tilde fence", raw: []byte("# Fence\n\n~~~text\n" + long + "\n~~~\n")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := chunkMarkdownForTest(t, tt.raw)

			require.Len(t, chunks, 1)
			require.Equal(t, string(tt.raw), chunks[0].Content)
			require.Equal(t, 1, chunks[0].Part)
			require.Equal(t, 1, chunks[0].PartCount)
			require.Equal(t, 1, chunks[0].StartLine)
			require.Equal(t, strings.Count(string(tt.raw), "\n"), chunks[0].EndLine)
		})
	}
}

func TestChunkMarkdown_ResetsHeadingHierarchyAcrossLevelChanges(t *testing.T) {
	raw := []byte("# A\nA body.\n\n### C\nC body.\n\n## B\nB body.\n\n# D\nD body.\n")

	chunks := chunkMarkdownForTest(t, raw)

	require.Equal(t, []string{"a", "c", "b", "d"}, chunkFragments(chunks))
	require.Equal(t, []string{"A"}, chunks[0].HeadingPath)
	require.Equal(t, []string{"A", "C"}, chunks[1].HeadingPath)
	require.Equal(t, []string{"A", "B"}, chunks[2].HeadingPath)
	require.Equal(t, []string{"D"}, chunks[3].HeadingPath)
}

func TestChunkMarkdown_InvalidParsedInputReturnsError(t *testing.T) {
	tests := []struct {
		name   string
		parsed *ParsedMarkdown
	}{
		{name: "nil parsed markdown", parsed: nil},
		{name: "missing AST", parsed: &ParsedMarkdown{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks, err := ChunkMarkdown(domain.SourceDocument{}, tt.parsed)

			require.Nil(t, chunks)
			require.Error(t, err)
		})
	}
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

func chunkIDs(chunks []domain.Chunk) []string {
	ids := make([]string, len(chunks))
	for index, chunk := range chunks {
		ids[index] = chunk.ID
	}
	return ids
}

func chunkURIs(chunks []domain.Chunk) []string {
	uris := make([]string, len(chunks))
	for index, chunk := range chunks {
		uris[index] = chunk.ChunkURI
	}
	return uris
}
