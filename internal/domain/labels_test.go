package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLabels_Union(t *testing.T) {
	tests := []struct {
		name       string
		pathLabels []string
		keywords   []string
		want       []string
	}{
		{name: "both nil", want: []string{}},
		{name: "both empty", pathLabels: []string{}, keywords: []string{}, want: []string{}},
		{name: "only blanks", pathLabels: []string{"", "  "}, keywords: []string{"\t"}, want: []string{}},
		{name: "path labels only", pathLabels: []string{"guides", "ops"}, want: []string{"guides", "ops"}},
		{name: "keywords only", keywords: []string{"docker", "compose"}, want: []string{"docker", "compose"}},
		{
			name:       "path labels precede keywords",
			pathLabels: []string{"zeta"},
			keywords:   []string{"alpha"},
			want:       []string{"zeta", "alpha"},
		},
		{
			name:       "lower-cased and trimmed",
			pathLabels: []string{" Guides "},
			keywords:   []string{"DOCKER\t", "Build Pipeline"},
			want:       []string{"guides", "docker", "build pipeline"},
		},
		{
			name:       "duplicate across sources keeps first-seen position",
			pathLabels: []string{"ops", "guides"},
			keywords:   []string{"Guides", "docker", "OPS"},
			want:       []string{"ops", "guides", "docker"},
		},
		{
			name:     "duplicate within keywords differing in case and spacing",
			keywords: []string{"Docker", "docker", " DOCKER ", "compose"},
			want:     []string{"docker", "compose"},
		},
		{
			name:       "empties dropped between real labels",
			pathLabels: []string{"", "a"},
			keywords:   []string{" ", "b", ""},
			want:       []string{"a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Labels(tt.pathLabels, tt.keywords)
			require.NotNil(t, got)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestLabels_DoesNotMutateInputs(t *testing.T) {
	pathLabels := []string{" Guides "}
	keywords := []string{"DOCKER"}

	Labels(pathLabels, keywords)

	require.Equal(t, []string{" Guides "}, pathLabels)
	require.Equal(t, []string{"DOCKER"}, keywords)
}

func TestChunk_Labels(t *testing.T) {
	chunk := Chunk{PathLabels: []string{"Guides", "ops"}, Keywords: []string{"OPS", " Docker "}}

	require.Equal(t, []string{"guides", "ops", "docker"}, chunk.Labels())
	require.Equal(t, []string{}, Chunk{}.Labels())
}

func TestSourceDocument_Labels(t *testing.T) {
	source := SourceDocument{PathLabels: []string{"Guides", "ops"}, Keywords: []string{"OPS", " Docker "}}

	require.Equal(t, []string{"guides", "ops", "docker"}, source.Labels())
	require.Equal(t, []string{}, SourceDocument{}.Labels())
}
