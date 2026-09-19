package vector

import (
	"strings"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestBuildPassages_PrefixesEveryPassageWithItsContext(t *testing.T) {
	chunk := domain.Chunk{
		ID:          "c1",
		SourceTitle: "Configuration",
		HeadingPath: []string{"Search", "Boosts"},
		Content:     strings.Repeat("alpha beta ", 400),
	}

	passages := BuildPassages(chunk, testModel("model-x", 0))

	require.Greater(t, len(passages), 1, "a 4400-rune body must split")
	for i, passage := range passages {
		require.Equal(t, "c1", passage.ChunkID)
		require.Equal(t, "Configuration > Search > Boosts", passage.Prefix)
		require.True(t, strings.HasPrefix(passage.Text, "Configuration > Search > Boosts"),
			"passage %d lost its context prefix", i)
		require.Contains(t, passage.Text, passage.Body)
	}
}

func TestBuildPassages_WindowsOverlap(t *testing.T) {
	chunk := domain.Chunk{ID: "c1", SourceTitle: "T", Content: strings.Repeat("x", 3000)}

	passages := BuildPassages(chunk, testModel("model-x", 0))

	require.Greater(t, len(passages), 2)
	totalBody := 0
	for _, passage := range passages {
		totalBody += len([]rune(passage.Body))
	}
	require.Greater(t, totalBody, 3000, "overlapping windows must cover more than the body length")
}

func TestBuildPassages_CoversTheWholeBody(t *testing.T) {
	body := strings.Repeat("abcdefghij", 500)
	chunk := domain.Chunk{ID: "c1", SourceTitle: "T", Content: body}

	passages := BuildPassages(chunk, testModel("model-x", 0))

	require.NotEmpty(t, passages)
	require.True(t, strings.HasPrefix(body, passages[0].Body))
	require.True(t, strings.HasSuffix(body, passages[len(passages)-1].Body))
}

func TestBuildPassages_ShrinksTheWindowToTheModelWindow(t *testing.T) {
	chunk := domain.Chunk{ID: "c1", SourceTitle: "T", Content: strings.Repeat("x", 3000)}

	wide := BuildPassages(chunk, testModel("model-x", 0))
	narrow := BuildPassages(chunk, testModel("model-x", 64))

	require.Greater(t, len(narrow), len(wide),
		"a 64-token window must produce more, smaller passages than the precision window")
	for _, passage := range narrow {
		require.LessOrEqual(t, len([]rune(passage.Text)), 64*RunesPerToken)
	}
}

func TestBuildPassages_CapsAPathologicalHeadingPrefix(t *testing.T) {
	chunk := domain.Chunk{
		ID:          "c1",
		SourceTitle: strings.Repeat("T", 200),
		HeadingPath: []string{strings.Repeat("H", 200), strings.Repeat("I", 200)},
		Content:     strings.Repeat("x", 2000),
	}

	passages := BuildPassages(chunk, testModel("model-x", 0))

	require.NotEmpty(t, passages)
	for _, passage := range passages {
		require.LessOrEqual(t, len([]rune(passage.Prefix)), PrefixBudgetRunes(0),
			"a deep heading path must not crowd out the content it contextualizes")
		require.NotEmpty(t, passage.Body)
	}
}

// The 16404-rune outlier measured in the real corpus. It must split cleanly
// rather than blow up or produce one truncated passage.
func TestBuildPassages_HandlesTheLargestMeasuredChunk(t *testing.T) {
	chunk := domain.Chunk{ID: "big", SourceTitle: "Big", Content: strings.Repeat("y", 16404)}

	passages := BuildPassages(chunk, testModel("model-x", 0))

	require.Greater(t, len(passages), 25)
	for _, passage := range passages {
		require.LessOrEqual(t, len([]rune(passage.Text)), PrecisionWindow*RunesPerToken)
	}
}

func TestBuildPassages_ShortChunkYieldsOnePassage(t *testing.T) {
	chunk := domain.Chunk{ID: "c1", SourceTitle: "T", HeadingPath: []string{"H"}, Content: "a short body"}

	passages := BuildPassages(chunk, testModel("model-x", 0))

	require.Len(t, passages, 1)
	require.Equal(t, "T > H", passages[0].Prefix)
	require.Equal(t, "a short body", passages[0].Body)
}

func TestBuildPassages_EmptyBodyStillCarriesContext(t *testing.T) {
	withContext := BuildPassages(domain.Chunk{ID: "c1", SourceTitle: "T", Content: "   "}, testModel("model-x", 0))
	require.Len(t, withContext, 1)
	require.Equal(t, "T", withContext[0].Text)

	require.Empty(t, BuildPassages(domain.Chunk{ID: "c1"}, testModel("model-x", 0)))
}

func TestPassageKey_IsSaltedByModelAndSensitiveToText(t *testing.T) {
	first := testModel("model-a", 0)
	second := testModel("model-b", 0)

	require.NotEqual(t, PassageKey(first, "text"), PassageKey(second, "text"))
	require.NotEqual(t, PassageKey(first, "text"), PassageKey(first, "texu"))
	require.Equal(t, PassageKey(first, "text"), PassageKey(first, "text"))
	// The separator must make the split unambiguous.
	require.NotEqual(t, PassageKey(testModel("ab", 0), "c"), PassageKey(testModel("a", 0), "bc"))
}

// A model swapped behind a stable configured path keeps its identifier and
// changes its width, so width has to reach the key on its own.
func TestPassageKey_IsSaltedByVectorWidth(t *testing.T) {
	narrow := Model{ID: "model-a", Dimensions: 384}
	wide := Model{ID: "model-a", Dimensions: 768}

	require.NotEqual(t, PassageKey(narrow, "text"), PassageKey(wide, "text"))
	require.Equal(t, PassageKey(narrow, "text"), PassageKey(Model{ID: "model-a", Dimensions: 384}, "text"))
	// The separator must keep the width from bleeding into the identifier.
	require.NotEqual(t, PassageKey(Model{ID: "m1", Dimensions: 6}, "t"), PassageKey(Model{ID: "m", Dimensions: 16}, "t"))
}

// The window reaches the key through the text it splits, so salting on it as
// well would discard cached vectors whose input never changed.
func TestPassageKey_IgnoresTheModelWindow(t *testing.T) {
	require.Equal(t,
		PassageKey(Model{ID: "m", Dimensions: 8, MaxTokens: 128}, "text"),
		PassageKey(Model{ID: "m", Dimensions: 8, MaxTokens: 512}, "text"))
}

func TestBuildPassages_KeysCarryTheVectorWidth(t *testing.T) {
	chunk := domain.Chunk{ID: "c1", SourceTitle: "T", Content: "body"}

	narrow := BuildPassages(chunk, Model{ID: "m", Dimensions: 384})
	wide := BuildPassages(chunk, Model{ID: "m", Dimensions: 768})

	require.Equal(t, narrow[0].Text, wide[0].Text, "only the width differs")
	require.NotEqual(t, narrow[0].Key, wide[0].Key)
}

func TestBuildPassages_KeyMatchesTheExactEmbedderInput(t *testing.T) {
	chunk := domain.Chunk{ID: "c1", SourceTitle: "T", HeadingPath: []string{"H"}, Content: "body"}

	passages := BuildPassages(chunk, testModel("model-x", 0))

	require.Len(t, passages, 1)
	require.Equal(t, PassageKey(testModel("model-x", 0), passages[0].Text), passages[0].Key)
}

// Two chunks with identical bodies under different headings must not share a
// key: the prefix is part of what gets embedded.
func TestBuildPassages_IdenticalBodiesUnderDifferentHeadingsDiffer(t *testing.T) {
	first := BuildPassages(domain.Chunk{ID: "a", SourceTitle: "T", HeadingPath: []string{"One"}, Content: "same"}, testModel("m", 0))
	second := BuildPassages(domain.Chunk{ID: "b", SourceTitle: "T", HeadingPath: []string{"Two"}, Content: "same"}, testModel("m", 0))

	require.NotEqual(t, first[0].Key, second[0].Key)
}

// Identical passages across documents dedup, so repeated boilerplate embeds once.
func TestBuildPassages_IdenticalContextAndBodyShareAKey(t *testing.T) {
	first := BuildPassages(domain.Chunk{ID: "a", SourceTitle: "T", Content: "same"}, testModel("m", 0))
	second := BuildPassages(domain.Chunk{ID: "b", SourceTitle: "T", Content: "same"}, testModel("m", 0))

	require.Equal(t, first[0].Key, second[0].Key)
}

func TestPassage_HalveSplitsTheBodyAndKeepsTheContext(t *testing.T) {
	passages := BuildPassages(domain.Chunk{ID: "c1", SourceTitle: "T", Content: "abcdefgh"}, testModel("m", 0))
	require.Len(t, passages, 1)

	first, second, ok := passages[0].Halve()

	require.True(t, ok)
	require.Equal(t, "abcd", first.Body)
	require.Equal(t, "efgh", second.Body)
	require.Equal(t, "T", first.Prefix)
	require.Equal(t, "T", second.Prefix)
	require.Equal(t, "c1", first.ChunkID)
	require.Equal(t, PassageKey(testModel("m", 0), first.Text), first.Key, "a halved passage rekeys on its own text")
	require.NotEqual(t, passages[0].Key, first.Key)
}

func TestPassage_HalveReportsAnIndivisibleBody(t *testing.T) {
	passages := BuildPassages(domain.Chunk{ID: "c1", SourceTitle: "T", Content: "x"}, testModel("m", 0))
	require.Len(t, passages, 1)

	_, _, ok := passages[0].Halve()

	require.False(t, ok, "a single-rune body is the skip floor")
}

func TestPassage_HalveSplitsOnRuneBoundaries(t *testing.T) {
	passages := BuildPassages(domain.Chunk{ID: "c1", SourceTitle: "T", Content: "日本語です"}, testModel("m", 0))

	first, second, ok := passages[0].Halve()

	require.True(t, ok)
	require.Equal(t, "日本", first.Body)
	require.Equal(t, "語です", second.Body)
	require.True(t, len(first.Body) > 0 && len(second.Body) > 0)
}

// A heading path that overshoots the budget by a modest margin must lose its
// middle, not its tail: the deepest heading is half of what makes the prefix
// discriminating, and dropping it silently rekeys every deep passage.
func TestBuildPassages_ElidesTheMiddleOfADeepHeadingPath(t *testing.T) {
	title := "Configuration Reference"
	headings := []string{
		"Search Backend Selection",
		"Semantic Retrieval Pipeline",
		"Embedding Adapter Contracts",
		"Cache Key Invalidation Rules",
		"Model Salted Passage Keys",
	}
	full := strings.Join(append([]string{title}, headings...), " > ")
	require.Greater(t, len([]rune(full)), PrefixBudgetRunes(0),
		"fixture must overshoot the budget or the elision branch never runs")

	chunk := domain.Chunk{ID: "c1", SourceTitle: title, HeadingPath: headings, Content: "a short body"}

	passages := BuildPassages(chunk, testModel("model-x", 0))

	require.Len(t, passages, 1)
	require.Equal(t, "Configuration Reference > … > Model Salted Passage Keys", passages[0].Prefix)
}

func TestBuildPassages_ChunkWithoutContextEmbedsTheBodyAlone(t *testing.T) {
	passages := BuildPassages(domain.Chunk{ID: "c1", Content: "body text"}, testModel("m", 0))

	require.Len(t, passages, 1)
	require.Equal(t, "body text", passages[0].Text, "no prefix means no leading separator")
	require.Equal(t, PassageKey(testModel("m", 0), "body text"), passages[0].Key)
}

func testModel(id string, maxTokens int) Model {
	return Model{ID: id, Dimensions: 8, MaxTokens: maxTokens}
}
