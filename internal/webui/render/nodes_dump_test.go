package render

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yuin/goldmark/ast"
)

func TestCustomNodes_DumpExposesTheirAttributes(t *testing.T) {
	tests := []struct {
		name string
		node ast.Node
		want []string
	}{
		{name: "broken link", node: &brokenLink{target: "missing.md"}, want: []string{"BrokenLink", "Target: missing.md"}},
		{name: "image placeholder", node: &imagePlaceholder{src: "logo.png", altHTML: "the logo"}, want: []string{"ImagePlaceholder", "Src: logo.png", "AltHTML: the logo"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dump := captureStdout(t, func() { tt.node.Dump(nil, 0) })

			for _, want := range tt.want {
				require.Contains(t, dump, want)
			}
		})
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	original := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = original }()

	fn()

	require.NoError(t, writer.Close())
	out, err := io.ReadAll(reader)
	require.NoError(t, err)
	return string(out)
}
