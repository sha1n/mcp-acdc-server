package render

import (
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yuin/goldmark/parser"
)

const detachedPaddingRows = 4096

// detachedSource puts the definition first so that any slice still pointing into
// the parse buffer has a capacity reaching across the whole padding that follows.
func detachedSource() string {
	var b strings.Builder
	b.WriteString("[Ref]: https://example.com/target \"Ref Title\"\n\n| a | b |\n| - | - |\n")
	for range detachedPaddingRows {
		b.WriteString("| padding cell content | more padding cell content |\n")
	}
	return b.String()
}

func TestCollectReferences_StoresStandaloneReferenceValues(t *testing.T) {
	standalone := reflect.TypeOf(parser.NewReference(nil, nil, nil))

	references := CollectReferences(detachedSource())

	require.Len(t, references.definitions, 1)
	for _, definition := range references.definitions {
		assert.Equal(t, standalone.String(), reflect.TypeOf(definition).String())
	}
}

func TestCollectReferences_DetachesValuesFromSourceBuffer(t *testing.T) {
	source := detachedSource()

	references := CollectReferences(source)

	require.Len(t, references.definitions, 1)
	definition := references.definitions[0]
	assert.Equal(t, "Ref", string(definition.Label()))
	assert.Equal(t, "https://example.com/target", string(definition.Destination()))
	assert.Equal(t, "Ref Title", string(definition.Title()))

	retainedBound := len(source) / 2
	for name, value := range map[string][]byte{
		"label":       definition.Label(),
		"destination": definition.Destination(),
		"title":       definition.Title(),
	} {
		assert.Lessf(t, cap(value), retainedBound, "%s still backed by the parse buffer", name)
	}
}

func TestCollectReferences_ValuesAreIndependentAcrossCollections(t *testing.T) {
	source := detachedSource()
	first := CollectReferences(source)
	second := CollectReferences(source)
	require.Len(t, first.definitions, 1)
	require.Len(t, second.definitions, 1)

	destination := first.definitions[0].Destination()
	for i := range destination {
		destination[i] = 'x'
	}

	assert.Equal(t, "https://example.com/target", string(second.definitions[0].Destination()))
}

func BenchmarkCollectReferences_RetainedHeap(b *testing.B) {
	source := detachedSource()
	var before, after runtime.MemStats
	retained := make([]References, b.N)
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := range b.N {
		retained[i] = CollectReferences(source)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	b.ReportMetric(float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/float64(b.N), "retained-B/op")
	runtime.KeepAlive(retained)
}
