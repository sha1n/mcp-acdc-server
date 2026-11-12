package search

import (
	"errors"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/stretchr/testify/require"
)

// labelFixture is indexed in reverse of its expected browse order so that any
// ordering a test observes comes from the sort, not from insertion order.
// Label counts: deployment 2, docker 2, docs 2, guides 2, build pipeline 1, ops 1.
func labelFixture() []domain.Chunk {
	return []domain.Chunk{
		{
			ID: "c#runbook", SourceURI: "acdc://guides/c", ChunkURI: "acdc://guides/c#runbook",
			SourceTitle: "Gamma", SourcePath: "guides/c.md",
			PathLabels: []string{"guides", "ops"}, Keywords: []string{"Ops"},
			StartLine: 1, EndLine: 3,
			Content: "Monitoring and alerting runbook.",
		},
		{
			ID: "b#build", SourceURI: "acdc://guides/b", ChunkURI: "acdc://guides/b#build",
			SourceTitle: "Beta", SourcePath: "guides/b.md",
			PathLabels: []string{"guides"}, Keywords: []string{"docker", "Build Pipeline"},
			HeadingPath: []string{"Beta", "Build"},
			StartLine:   1, EndLine: 5,
			Content: "Docker image build pipeline.",
		},
		{
			ID: "a#scale", SourceURI: "acdc://docs/a", ChunkURI: "acdc://docs/a#scale",
			SourceTitle: "Alpha", SourcePath: "docs/a.md",
			PathLabels: []string{"docs"}, Keywords: []string{"deployment"},
			HeadingPath: []string{"Alpha", "Scale"},
			StartLine:   12, EndLine: 20,
			Content: "Kubernetes deployment scaling.",
		},
		{
			ID: "a#intro", SourceURI: "acdc://docs/a", ChunkURI: "acdc://docs/a#intro",
			SourceTitle: "Alpha", SourcePath: "docs/a.md",
			PathLabels: []string{"docs"}, Keywords: []string{"Deployment", "Docker"},
			HeadingPath: []string{"Alpha", "Intro"},
			StartLine:   9, EndLine: 11,
			Content: "Docker compose deployment walkthrough.",
		},
	}
}

func labelServiceWith(t *testing.T, settings config.SearchSettings, chunks []domain.Chunk) *Service {
	t.Helper()
	service := NewService(settings)
	t.Cleanup(service.Close)
	indexChunks(t, service, chunks)
	return service
}

func labelService(t *testing.T) *Service {
	t.Helper()
	return labelServiceWith(t, testSettings(), labelFixture())
}

func labelledIDs(results []LabelledResult) []string {
	ids := make([]string, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.ChunkID)
	}
	return ids
}

func fixtureLabels() map[string][]string {
	labels := make(map[string][]string)
	for _, chunk := range labelFixture() {
		labels[chunk.ID] = chunk.Labels()
	}
	return labels
}

func carriesAll(have, want []string) bool {
	set := make(map[string]bool, len(have))
	for _, label := range have {
		set[label] = true
	}
	for _, label := range want {
		if !set[label] {
			return false
		}
	}
	return true
}

func TestLabelledSearch_WithoutIndexReturnsEmpty(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()

	for _, tc := range []struct {
		query  string
		labels []string
	}{
		{"docker", nil},
		{"", []string{"docker"}},
		{"docker", []string{"docker"}},
	} {
		results, err := service.LabelledSearch(tc.query, tc.labels, 10)
		require.NoError(t, err)
		require.NotNil(t, results)
		require.Empty(t, results)
	}
}

func TestLabelledSearch_EmptyQueryWithoutLabelsReturnsEmpty(t *testing.T) {
	service := labelService(t)

	tests := []struct {
		name   string
		query  string
		labels []string
	}{
		{name: "nil labels", query: "", labels: nil},
		{name: "empty labels", query: "", labels: []string{}},
		{name: "blank query and blank labels", query: "   ", labels: []string{"", "  "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := service.LabelledSearch(tt.query, tt.labels, 10)
			require.NoError(t, err)
			require.NotNil(t, results)
			require.Empty(t, results)
		})
	}
}

func TestLabelledSearch_TextQueryMatchesSearchRestrictedToLabels(t *testing.T) {
	service := labelService(t)
	fixture := fixtureLabels()

	tests := []struct {
		name   string
		query  string
		labels []string
	}{
		{name: "no labels", query: "docker"},
		{name: "no labels multi-term", query: "deployment walkthrough"},
		{name: "match all", query: "*"},
		{name: "match all narrowed", query: "*", labels: []string{"guides"}},
		{name: "single label", query: "docker", labels: []string{"guides"}},
		{name: "two labels", query: "deployment", labels: []string{"docs", "docker"}},
		{name: "label matching nothing", query: "docker", labels: []string{"ops"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plain, err := service.Search(tt.query, 100)
			require.NoError(t, err)
			require.NotEmpty(t, plain, "query %q must retrieve something for the comparison to mean anything", tt.query)

			var want []SearchResult
			for _, result := range plain {
				if carriesAll(fixture[result.ChunkID], tt.labels) {
					want = append(want, result)
				}
			}

			got, err := service.LabelledSearch(tt.query, tt.labels, 100)
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Len(t, got, len(want))
			for i := range want {
				require.Equal(t, want[i].ChunkID, got[i].ChunkID, "rank %d", i+1)
				require.InDelta(t, want[i].Score, got[i].Score, 1e-9, "score at rank %d", i+1)
			}
		})
	}
}

func TestLabelledSearch_TrimsQuery(t *testing.T) {
	service := labelService(t)

	trimmed, err := service.LabelledSearch("docker", nil, 10)
	require.NoError(t, err)
	require.NotEmpty(t, trimmed)

	padded, err := service.LabelledSearch("  docker \t", nil, 10)
	require.NoError(t, err)
	require.Equal(t, trimmed, padded)
}

func TestLabelledSearch_LabelFilterIsCaseInsensitiveAndExact(t *testing.T) {
	service := labelService(t)

	tests := []struct {
		name   string
		labels []string
		want   []string
	}{
		{name: "lower case", labels: []string{"docker"}, want: []string{"a#intro", "b#build"}},
		{name: "upper case", labels: []string{"DOCKER"}, want: []string{"a#intro", "b#build"}},
		{name: "padded mixed case", labels: []string{" Docker "}, want: []string{"a#intro", "b#build"}},
		{name: "duplicates collapse", labels: []string{"docker", "Docker", "DOCKER"}, want: []string{"a#intro", "b#build"}},
		{name: "blank labels ignored", labels: []string{"", "docker", " "}, want: []string{"a#intro", "b#build"}},
		{name: "multi-word label", labels: []string{"Build Pipeline"}, want: []string{"b#build"}},
		{name: "prefix is not a match", labels: []string{"dock"}, want: []string{}},
		{name: "word of multi-word label is not a match", labels: []string{"build"}, want: []string{}},
		{name: "unknown label", labels: []string{"nope"}, want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := service.LabelledSearch("", tt.labels, 10)
			require.NoError(t, err)
			require.NotNil(t, results)
			require.Equal(t, tt.want, labelledIDs(results))
		})
	}
}

func TestLabelledSearch_LabelsCombineWithAnd(t *testing.T) {
	service := labelService(t)

	tests := []struct {
		name   string
		labels []string
		want   []string
	}{
		{name: "one label", labels: []string{"guides"}, want: []string{"b#build", "c#runbook"}},
		{name: "second label narrows", labels: []string{"guides", "docker"}, want: []string{"b#build"}},
		{name: "order does not matter", labels: []string{"docker", "guides"}, want: []string{"b#build"}},
		{name: "disjoint labels", labels: []string{"docker", "ops"}, want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := service.LabelledSearch("", tt.labels, 10)
			require.NoError(t, err)
			require.Equal(t, tt.want, labelledIDs(results))
		})
	}
}

func TestLabelledSearch_LabelOnlyBrowseOrdersBySourceThenLine(t *testing.T) {
	service := labelService(t)

	tests := []struct {
		name  string
		label string
		want  []string
	}{
		{name: "same source by start line", label: "docs", want: []string{"a#intro", "a#scale"}},
		{name: "across sources by source uri", label: "guides", want: []string{"b#build", "c#runbook"}},
		{name: "source uri before start line", label: "docker", want: []string{"a#intro", "b#build"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := service.LabelledSearch("", []string{tt.label}, 10)
			require.NoError(t, err)
			require.Equal(t, tt.want, labelledIDs(results))
			for _, result := range results {
				require.Zero(t, result.Score, "label-only browse is unscored")
			}
		})
	}
}

func TestLabelledSearch_PopulatesResultFields(t *testing.T) {
	service := labelService(t)

	t.Run("label-only browse", func(t *testing.T) {
		results, err := service.LabelledSearch("", []string{"OPS"}, 10)
		require.NoError(t, err)
		require.Equal(t, []LabelledResult{{
			ChunkID:     "c#runbook",
			SourceURI:   "acdc://guides/c",
			ChunkURI:    "acdc://guides/c#runbook",
			SourceTitle: "Gamma",
			SourcePath:  "guides/c.md",
			HeadingPath: []string{},
			Labels:      []string{"guides", "ops"},
			StartLine:   1,
			EndLine:     3,
			Score:       0,
			Snippet:     []SnippetSpan{{Text: "Monitoring and alerting runbook.", Hit: false}},
			Content:     "Monitoring and alerting runbook.",
		}}, results)
	})

	t.Run("text query", func(t *testing.T) {
		results, err := service.LabelledSearch("walkthrough", nil, 10)
		require.NoError(t, err)
		require.NotEmpty(t, results)

		got := results[0]
		require.Equal(t, "a#intro", got.ChunkID)
		require.Equal(t, "acdc://docs/a", got.SourceURI)
		require.Equal(t, "acdc://docs/a#intro", got.ChunkURI)
		require.Equal(t, "Alpha", got.SourceTitle)
		require.Equal(t, "docs/a.md", got.SourcePath)
		require.Equal(t, []string{"Alpha", "Intro"}, got.HeadingPath)
		require.Equal(t, []string{"docs", "deployment", "docker"}, got.Labels)
		require.Equal(t, 9, got.StartLine)
		require.Equal(t, 11, got.EndLine)
		require.Positive(t, got.Score)
		require.NotEmpty(t, got.Snippet)
		require.Equal(t, "Docker compose deployment walkthrough.", got.Content)
	})

	t.Run("chunk without labels", func(t *testing.T) {
		unlabelled := labelServiceWith(t, testSettings(), []domain.Chunk{{
			ID: "plain", SourceURI: "acdc://plain", ChunkURI: "acdc://plain", Content: "Unlabelled prose.",
		}})

		results, err := unlabelled.LabelledSearch("prose", nil, 10)
		require.NoError(t, err)
		require.Len(t, results, 1)
		require.NotNil(t, results[0].Labels)
		require.Empty(t, results[0].Labels)
		require.NotNil(t, results[0].HeadingPath)
		require.Empty(t, results[0].HeadingPath)
	})
}

func TestLabelledSearch_Limit(t *testing.T) {
	settings := testSettings()
	settings.MaxResults = 1
	service := labelServiceWith(t, settings, labelFixture())

	tests := []struct {
		name   string
		query  string
		labels []string
		limit  int
		want   int
	}{
		{name: "zero falls back to settings on browse", labels: []string{"guides"}, limit: 0, want: 1},
		{name: "negative falls back to settings on browse", labels: []string{"guides"}, limit: -1, want: 1},
		{name: "explicit limit on browse", labels: []string{"guides"}, limit: 2, want: 2},
		{name: "zero falls back to settings on query", query: "docker", limit: 0, want: 1},
		{name: "explicit limit on query", query: "docker", limit: 2, want: 2},
		{name: "limit above matches", labels: []string{"guides"}, limit: 50, want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := service.LabelledSearch(tt.query, tt.labels, tt.limit)
			require.NoError(t, err)
			require.Len(t, results, tt.want)
		})
	}
}

func TestLabelledSearch_ReportsSearchFailure(t *testing.T) {
	backing, err := bleve.NewMemOnly(buildMapping())
	require.NoError(t, err)
	service := NewService(testSettings())
	defer service.Close()
	service.index = &failingSearchIndex{backing: backing, searchErr: errors.New("boom")}

	for _, tc := range []struct {
		query  string
		labels []string
	}{
		{"docker", nil},
		{"", []string{"docker"}},
	} {
		results, err := service.LabelledSearch(tc.query, tc.labels, 10)
		require.ErrorContains(t, err, "boom")
		require.Nil(t, results)
	}
}

// TestLabelledSearch_LabelsAreNotComposedIntoAll guards the labels field's
// IncludeInAll=false: a keyword-analysed label leaking into _all would add a
// whole-value token no query reads.
func TestLabelledSearch_LabelsAreNotComposedIntoAll(t *testing.T) {
	service := labelService(t)

	probe := bleve.NewTermQuery("build pipeline")
	probe.SetField("_all")
	result, err := service.index.Search(bleve.NewSearchRequest(probe))
	require.NoError(t, err)
	require.Empty(t, result.Hits)
}

func TestLabels_WithoutIndexReturnsEmpty(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()

	labels, err := service.Labels("", 0)
	require.NoError(t, err)
	require.NotNil(t, labels)
	require.Empty(t, labels)
}

func TestLabels_CountsOrderPrefixAndLimit(t *testing.T) {
	service := labelService(t)

	all := []LabelCount{
		{Name: "deployment", Count: 2},
		{Name: "docker", Count: 2},
		{Name: "docs", Count: 2},
		{Name: "guides", Count: 2},
		{Name: "build pipeline", Count: 1},
		{Name: "ops", Count: 1},
	}

	tests := []struct {
		name   string
		prefix string
		limit  int
		want   []LabelCount
	}{
		{name: "all with zero limit", limit: 0, want: all},
		{name: "all with negative limit", limit: -1, want: all},
		{name: "limit above total", limit: 100, want: all},
		{name: "limit truncates after ordering", limit: 3, want: all[:3]},
		{name: "prefix", prefix: "do", want: []LabelCount{{Name: "docker", Count: 2}, {Name: "docs", Count: 2}}},
		{name: "prefix normalised", prefix: " DO ", want: []LabelCount{{Name: "docker", Count: 2}, {Name: "docs", Count: 2}}},
		{name: "prefix and limit", prefix: "do", limit: 1, want: []LabelCount{{Name: "docker", Count: 2}}},
		{name: "multi-word prefix", prefix: "Build P", want: []LabelCount{{Name: "build pipeline", Count: 1}}},
		{name: "full name as prefix", prefix: "ops", want: []LabelCount{{Name: "ops", Count: 1}}},
		{name: "inner word is not a prefix", prefix: "pipeline", want: []LabelCount{}},
		{name: "no match", prefix: "zzz", want: []LabelCount{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := service.Labels(tt.prefix, tt.limit)
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestLabels_FollowsReindex(t *testing.T) {
	service := labelService(t)

	indexChunks(t, service, labelFixture()[:1])

	got, err := service.Labels("", 0)
	require.NoError(t, err)
	require.Equal(t, []LabelCount{{Name: "guides", Count: 1}, {Name: "ops", Count: 1}}, got)
}
