package search

import (
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/domain"
	"github.com/stretchr/testify/require"
)

func labeledChunks() []domain.Chunk {
	return []domain.Chunk{
		{
			ID: "compose#deploy", SourceURI: "acdc://compose", ChunkURI: "acdc://compose#deploy",
			SourceTitle: "Compose", SourcePath: "guides/deployment/compose.md",
			PathLabels: []string{"guides", "deployment"}, Keywords: []string{"docker", "deployment"},
			Content: "Deploy containers with docker compose.",
		},
		{
			ID: "k8s#pods", SourceURI: "acdc://k8s", ChunkURI: "acdc://k8s#pods",
			SourceTitle: "Kubernetes", SourcePath: "guides/k8s.md",
			PathLabels: []string{"guides"}, Keywords: []string{"kubernetes"},
			Content: "Run pods built from docker images on a cluster.",
		},
		{
			ID: "cli#ref", SourceURI: "acdc://cli", ChunkURI: "acdc://cli#ref",
			SourceTitle: "CLI", SourcePath: "reference/cli.md",
			PathLabels: []string{"reference"}, Keywords: []string{"docker"},
			Content: "Docker command line reference.",
		},
		{
			ID: "rollback#steps", SourceURI: "acdc://rollback", ChunkURI: "acdc://rollback#steps",
			SourceTitle: "Rollback", SourcePath: "deployment/rollback.md",
			PathLabels: []string{"deployment"},
			Content:    "Rollback procedure for failed releases.",
		},
	}
}

func newLabeledService(t *testing.T) *Service {
	t.Helper()
	service := NewService(testSettings())
	t.Cleanup(service.Close)
	indexChunks(t, service, labeledChunks())
	return service
}

func TestSearchLabeled_LabelsAreDedupedUnionOfPathLabelsThenKeywords(t *testing.T) {
	service := newLabeledService(t)

	results, err := service.SearchLabeled("compose", nil, 10)

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "compose#deploy", results[0].ChunkID)
	require.Equal(t, []string{"guides", "deployment", "docker"}, results[0].Labels)
}

func TestSearchLabeled_NoLabelsMatchesQueryLikeSearch(t *testing.T) {
	service := newLabeledService(t)

	labeled, err := service.SearchLabeled("docker", nil, 10)
	require.NoError(t, err)
	plain, err := service.Search("docker", 10)
	require.NoError(t, err)

	require.ElementsMatch(t, []string{"compose#deploy", "k8s#pods", "cli#ref"}, chunkIDs(labeled))
	require.Equal(t, chunkIDs(plain), chunkIDs(labeled))
}

func TestSearchLabeled_FiltersByLabel(t *testing.T) {
	service := newLabeledService(t)

	results, err := service.SearchLabeled("docker", []string{"guides"}, 10)

	require.NoError(t, err)
	require.ElementsMatch(t, []string{"compose#deploy", "k8s#pods"}, chunkIDs(results))
}

func TestSearchLabeled_MultipleLabelsUseANDSemantics(t *testing.T) {
	service := newLabeledService(t)

	results, err := service.SearchLabeled("docker", []string{"guides", "deployment"}, 10)

	require.NoError(t, err)
	require.Equal(t, []string{"compose#deploy"}, chunkIDs(results))
}

func TestSearchLabeled_LabelFilterIsCaseInsensitive(t *testing.T) {
	service := newLabeledService(t)

	results, err := service.SearchLabeled("docker", []string{"GUIDES", "Deployment"}, 10)

	require.NoError(t, err)
	require.Equal(t, []string{"compose#deploy"}, chunkIDs(results))
}

func TestSearchLabeled_LabelFilterIsExactNotPrefixOrStem(t *testing.T) {
	service := newLabeledService(t)

	for _, label := range []string{"deploy", "guide", "dock"} {
		results, err := service.SearchLabeled("", []string{label}, 10)

		require.NoError(t, err, label)
		require.Empty(t, results, label)
	}
}

func TestSearchLabeled_LabelFromKeywordsMatches(t *testing.T) {
	service := newLabeledService(t)

	results, err := service.SearchLabeled("", []string{"kubernetes"}, 10)

	require.NoError(t, err)
	require.Equal(t, []string{"k8s#pods"}, chunkIDs(results))
}

func TestSearchLabeled_EmptyQueryWithLabelsReturnsAllLabeledChunks(t *testing.T) {
	service := newLabeledService(t)

	results, err := service.SearchLabeled("", []string{"deployment"}, 10)

	require.NoError(t, err)
	require.ElementsMatch(t, []string{"compose#deploy", "rollback#steps"}, chunkIDs(results))
}

func TestSearchLabeled_EmptyQueryWithoutLabelsReturnsNoResults(t *testing.T) {
	service := newLabeledService(t)

	results, err := service.SearchLabeled("", nil, 10)

	require.NoError(t, err)
	require.NotNil(t, results)
	require.Empty(t, results)
}

func TestSearchLabeled_UnknownLabelReturnsNoResults(t *testing.T) {
	service := newLabeledService(t)

	results, err := service.SearchLabeled("docker", []string{"nonexistent"}, 10)

	require.NoError(t, err)
	require.NotNil(t, results)
	require.Empty(t, results)
}

func TestSearchLabeled_RespectsLimit(t *testing.T) {
	service := newLabeledService(t)

	results, err := service.SearchLabeled("", []string{"guides"}, 1)

	require.NoError(t, err)
	require.Len(t, results, 1)
}

func TestSearchLabeled_EmptyIndex(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()

	results, err := service.SearchLabeled("docker", []string{"guides"}, 10)

	require.NoError(t, err)
	require.NotNil(t, results)
	require.Empty(t, results)
}

func TestLabels_CountsChunksOrderedByCountDescThenNameAsc(t *testing.T) {
	service := newLabeledService(t)

	labels, err := service.Labels("", 10)

	require.NoError(t, err)
	require.Equal(t, []LabelCount{
		{Name: "deployment", Count: 2},
		{Name: "docker", Count: 2},
		{Name: "guides", Count: 2},
		{Name: "kubernetes", Count: 1},
		{Name: "reference", Count: 1},
	}, labels)
}

func TestLabels_RespectsLimit(t *testing.T) {
	service := newLabeledService(t)

	labels, err := service.Labels("", 2)

	require.NoError(t, err)
	require.Equal(t, []LabelCount{{Name: "deployment", Count: 2}, {Name: "docker", Count: 2}}, labels)
}

func TestLabels_PrefixFilterIsCaseInsensitive(t *testing.T) {
	service := newLabeledService(t)

	tests := []struct {
		prefix string
		want   []LabelCount
	}{
		{"d", []LabelCount{{Name: "deployment", Count: 2}, {Name: "docker", Count: 2}}},
		{"DO", []LabelCount{{Name: "docker", Count: 2}}},
		{"Kube", []LabelCount{{Name: "kubernetes", Count: 1}}},
		{"reference", []LabelCount{{Name: "reference", Count: 1}}},
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			labels, err := service.Labels(tt.prefix, 10)

			require.NoError(t, err)
			require.Equal(t, tt.want, labels)
		})
	}
}

func TestLabels_NoMatchReturnsEmpty(t *testing.T) {
	service := newLabeledService(t)

	labels, err := service.Labels("zzz", 10)

	require.NoError(t, err)
	require.NotNil(t, labels)
	require.Empty(t, labels)
}

func TestLabels_EmptyIndex(t *testing.T) {
	service := NewService(testSettings())
	defer service.Close()

	labels, err := service.Labels("", 10)

	require.NoError(t, err)
	require.NotNil(t, labels)
	require.Empty(t, labels)
}
