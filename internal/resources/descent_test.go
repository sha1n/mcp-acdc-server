package resources

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDescentFilter_AllowsOnlyDirectoriesAnIncludePatternCanReach(t *testing.T) {
	tests := []struct {
		name     string
		includes []string
		allowed  []string
		denied   []string
	}{
		{
			name:     "root anchored file pattern reaches no directory",
			includes: []string{"README.md"},
			denied:   []string{"docs", "src", "docs/api"},
		},
		{
			name:     "a single star does not cross a separator",
			includes: []string{"*.md"},
			denied:   []string{"docs", "docs/api"},
		},
		{
			name:     "a literal base admits its own subtree",
			includes: []string{"docs/**/*.md"},
			allowed:  []string{"docs", "docs/api", "docs/api/v1"},
			denied:   []string{"src", "documents"},
		},
		{
			name:     "a nested base admits its ancestors and its subtree",
			includes: []string{"a/b/*.md"},
			allowed:  []string{"a", "a/b", "a/b/c"},
			denied:   []string{"a/c", "b", "ab"},
		},
		{
			name:     "a leading globstar admits everything",
			includes: []string{"**/*.md"},
			allowed:  []string{"docs", "src/deep/deeper"},
		},
		{
			name:     "a bare globstar admits everything",
			includes: []string{"**"},
			allowed:  []string{"docs", "src/deep"},
		},
		{
			name:     "a wildcard first segment admits everything",
			includes: []string{"d*cs/**"},
			allowed:  []string{"docs", "src", "src/deep"},
		},
		{
			name:     "a brace in the first segment admits everything",
			includes: []string{"{docs,adr}/**/*.md"},
			allowed:  []string{"docs", "adr", "src"},
		},
		{
			name:     "relative segments resolve before the base is taken",
			includes: []string{"./docs/**/*.md"},
			allowed:  []string{"docs", "docs/api"},
			denied:   []string{"src"},
		},
		{
			name:     "a base that resolves back to the root falls back to the remainder",
			includes: []string{"a/../**/*.md"},
			allowed:  []string{"a", "src/deep"},
		},
		{
			name:     "escaped meta characters stay literal",
			includes: []string{`do\*cs/**`},
			allowed:  []string{"do*cs", "do*cs/api"},
			denied:   []string{"docs"},
		},
		{
			name:     "a base outside the content root admits nothing",
			includes: []string{"../outside/*.md"},
			denied:   []string{"outside", "docs"},
		},
		{
			name:     "several patterns admit their union",
			includes: []string{"README.md", "docs/**/*.md", "adr/*.md"},
			allowed:  []string{"docs", "docs/api", "adr"},
			denied:   []string{"src", "adr-drafts"},
		},
		{
			name:     "no include patterns admit nothing",
			includes: nil,
			denied:   []string{"docs"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := newDescentFilter(tt.includes)

			require.True(t, filter.allows("."), "the content root is never pruned")
			for _, dir := range tt.allowed {
				require.True(t, filter.allows(dir), "expected %q to be descended", dir)
			}
			for _, dir := range tt.denied {
				require.False(t, filter.allows(dir), "expected %q to be pruned", dir)
			}
		})
	}
}
