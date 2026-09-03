package integration

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/embed"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

// D7: the operator explicitly asked for semantic search, so a model that
// cannot be loaded aborts startup rather than running silently degraded.
// Until the adapter lands, every configured path is such a model.
func TestSemanticSearch_UnloadableModelAbortsStartup(t *testing.T) {
	contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"guide.md": "---\nname: Guide\ndescription: A guide\n---\nBody text.",
		},
	})
	flags := testkit.NewTestFlags(t, contentDir, &testkit.FlagOptions{Transport: "sse"})
	require.NoError(t, flags.Set("search-semantic-model", "/definitely/not/a/model"))

	env := testkit.NewTestEnv(testkit.NewACDCService("acdc-semantic-fail", flags))
	t.Cleanup(func() { _ = env.Stop() })

	_, err := env.Start()

	require.Error(t, err)
	require.Contains(t, err.Error(), "/definitely/not/a/model", "the operator must be told which path failed")
	require.Contains(t, err.Error(), "failed to load semantic model", "and why it failed")
}

// Declaring the flag without a value must not turn anything on: empty is off,
// and off is the runtime path the server has always taken.
func TestSemanticSearch_EmptyModelPathLeavesSearchUnchanged(t *testing.T) {
	client := testkit.NewStdioTestClient(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"auth-guide.md": "---\nname: Authentication Guide\ndescription: Guide for authentication\n---\nThis document covers authentication and security best practices.",
		},
	})
	defer client.Close()

	ctx := context.Background()

	found, err := client.CallTool(ctx, "search", map[string]any{"query": "authentication"})
	require.NoError(t, err)
	require.Contains(t, getTextContent(t, found), "Authentication Guide")

	// The floor exists so that semantic-on cannot start answering this with
	// the corpus's nearest-but-irrelevant chunk. Semantic-off must never.
	missing, err := client.CallTool(ctx, "search", map[string]any{"query": "nonexistentterm12345"})
	require.NoError(t, err)
	require.Contains(t, getTextContent(t, missing), "No results")
}

// semanticTestModel doubles as the passage cache-key salt, so it only has to
// be stable; nothing opens it as a file because the provider below ignores it.
const semanticTestModel = "testkit-scripted-model"

// scriptedEmbedder maps a substring of the embedded text to an explicit unit
// vector, so a query and a chunk that share no lexical term can be made
// deliberately close. embed.NewFake is hash-derived and therefore
// near-orthogonal by construction, which says nothing about retrieval.
type scriptedEmbedder struct {
	dimensions int
	vectors    map[string][]float32
}

func (s *scriptedEmbedder) Info() embed.ModelInfo {
	// No window: this embedder never truncates, so it owes no ErrInputTooLong.
	return embed.ModelInfo{Dimensions: s.dimensions}
}

func (s *scriptedEmbedder) lookup(text string) []float32 {
	for marker, vector := range s.vectors {
		if strings.Contains(text, marker) {
			return append([]float32(nil), vector...)
		}
	}
	// Orthogonal to every scripted vector, so an unscripted text matches nothing.
	orthogonal := make([]float32, s.dimensions)
	orthogonal[s.dimensions-1] = 1
	return orthogonal
}

func (s *scriptedEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		vectors[i] = s.lookup(text)
	}
	return vectors, nil
}

func (s *scriptedEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.lookup(text), nil
}

// "kestrel" embeds onto the rotation document's vector though it shares no
// term with either document, so a query that carries it (alone, or alongside
// a term the lexical side can find elsewhere) is the semantic side's doing
// for Credential Rotation specifically. Every indexed passage is scripted
// too, so nothing in the corpus falls back to the orthogonal vector — which
// is what an unscripted query embeds to, and would otherwise match a
// fallback chunk at cosine 1.
func semanticTestEmbedder() *scriptedEmbedder {
	return &scriptedEmbedder{
		dimensions: 3,
		vectors: map[string][]float32{
			"credentials": {1, 0, 0},
			"kestrel":     {1, 0, 0},
			"glaze":       {0, 1, 0},
		},
	}
}

// The two documents share no vocabulary with each other. "credentials" and
// "kestrel" never appear in either body; "finish" appears only in
// glazing.md, which is what lets a query built from both terms give the
// lexical and semantic sides different answers.
func semanticTestResources() map[string]string {
	return map[string]string{
		"rotation.md": "---\nname: Credential Rotation\ndescription: How operators rotate credentials\n---\nOperators rotate credentials every ninety days.",
		"glazing.md":  "---\nname: Pottery Glazing\ndescription: How a glaze is applied\n---\nKiln temperature decides the glaze finish.",
	}
}

func newSemanticSearchClient(t *testing.T) *testkit.TestClient {
	t.Helper()

	contentDir := testkit.CreateTestContentDir(t, &testkit.ContentDirOptions{
		Resources: semanticTestResources(),
	})
	flags := testkit.NewTestFlags(t, contentDir, &testkit.FlagOptions{Transport: "stdio"})
	require.NoError(t, flags.Set("search-semantic-model", semanticTestModel))
	// Pinned rather than left to the default, because the floor is what two of
	// these tests turn on.
	require.NoError(t, flags.Set("search-semantic-floor", fmt.Sprintf("%v", config.DefaultSemanticFloor)))

	embedder := semanticTestEmbedder()
	return testkit.NewStdioTestClientWithFlags(t, flags, testkit.WithEmbedderProvider(
		func(string) (embed.Embedder, error) { return embedder, nil },
	))
}

// chunkURIFrom extracts the chunk URI for a specific source (e.g. "rotation")
// rather than whichever URI leads the output, which under RRF is not
// something a test should have to depend on.
func chunkURIFrom(t *testing.T, searchOutput, source string) string {
	t.Helper()

	pattern := regexp.MustCompile(`\[(acdc://` + regexp.QuoteMeta(source) + `[^\]]*)\]`)
	match := pattern.FindStringSubmatch(searchOutput)
	require.Len(t, match, 2, "the search result must carry a linked chunk URI for %q:\n%s", source, searchOutput)
	return match[1]
}

// kestrelFinishQuery gives the lexical and semantic sides different answers:
// "finish" appears only in glazing.md, so the lexical side ranks Pottery
// Glazing and never ranks Credential Rotation at all; "kestrel" embeds onto
// Credential Rotation's vector, so the semantic side ranks it too. A query
// must never carry two scripted markers at once — scriptedEmbedder.lookup
// iterates a map, so a text holding both "kestrel" and "glaze" would resolve
// to whichever the iteration happens to visit first — and "finish" is not a
// scripted marker, so it is safe here.
const kestrelFinishQuery = "kestrel finish"

// D11 narrowed the semantic side's job: it no longer has to prove it can find
// a document lexical search cannot reach at all (that capability is retired,
// per D11), only that it still out-ranks a lexical result that exists but
// names the wrong document.
func TestSemanticSearch_ReturnsTheDocumentLexicalRanksNowhere(t *testing.T) {
	semantic := newSemanticSearchClient(t)
	defer semantic.Close()

	ctx := context.Background()

	found, err := semantic.CallTool(ctx, "search", map[string]any{"query": kestrelFinishQuery})
	require.NoError(t, err)
	require.Contains(t, getTextContent(t, found), "Credential Rotation")

	// The contrast is the assertion: same corpus, same query, semantic off.
	// The lexical side ranks the document its own term names, and only that
	// one.
	lexical := testkit.NewStdioTestClient(t, &testkit.ContentDirOptions{Resources: semanticTestResources()})
	defer lexical.Close()

	missing, err := lexical.CallTool(ctx, "search", map[string]any{"query": kestrelFinishQuery})
	require.NoError(t, err)
	lexicalOnly := getTextContent(t, missing)
	require.Contains(t, lexicalOnly, "Pottery Glazing")
	require.NotContains(t, lexicalOnly, "Credential Rotation",
		"only the semantic side can rank this query's right answer")
}

// Chunk boundaries, chunk URIs and the tool schemas are unchanged with
// semantic search on: a URI it returns must still read back through the read
// tool.
func TestSemanticSearch_ChunkURIFromASemanticHitStillReads(t *testing.T) {
	client := newSemanticSearchClient(t)
	defer client.Close()

	ctx := context.Background()

	found, err := client.CallTool(ctx, "search", map[string]any{"query": kestrelFinishQuery})
	require.NoError(t, err)
	// Lexical ranks Pottery Glazing and semantic ranks Credential Rotation, so
	// RRF ties them at score 1.00; which one leads the output is not
	// something this test should depend on, so it selects Credential
	// Rotation's URI specifically.
	uri := chunkURIFrom(t, getTextContent(t, found), "rotation")

	read, err := client.CallTool(ctx, "read", map[string]any{"uri": uri})
	require.NoError(t, err)
	require.Contains(t, getTextContent(t, read), "ninety days")
}

// D11: the semantic side cannot distinguish "no answer exists" from "an
// answer exists"; the lexical side can. "kestrel" alone embeds at cosine 1.0
// onto a real chunk, so nothing about the floor rejects it — only the
// lexical side returning no results, and D11 suppressing semantic in
// response, makes this query answer "No results found".
func TestSemanticSearch_EmptyLexicalResultSuppressesSemantic(t *testing.T) {
	client := newSemanticSearchClient(t)
	defer client.Close()

	missing, err := client.CallTool(context.Background(), "search", map[string]any{"query": "kestrel"})

	require.NoError(t, err)
	require.Contains(t, getTextContent(t, missing), "No results")
}

// normalizeScores exists so the presenter's "score %.2f" stays readable once
// RRF, whose raw scores cluster near 1/60, is in the path. Holds whichever
// document leads: RRF ties Pottery Glazing and Credential Rotation at 1.00.
func TestSemanticSearch_TopResultRendersANormalizedScore(t *testing.T) {
	client := newSemanticSearchClient(t)
	defer client.Close()

	found, err := client.CallTool(context.Background(), "search", map[string]any{"query": kestrelFinishQuery})

	require.NoError(t, err)
	require.Contains(t, getTextContent(t, found), "score 1.00")
}
