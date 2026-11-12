package render_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/webui/render"
	"github.com/stretchr/testify/require"
)

const (
	referenceSourceURI = "acdc://guides/setup"
	referenceChunk     = "See [Details][d]."
	referenceDocument  = "# Intro\n\nSee [Details][d].\n\n# Details\n\nSection body text.\n\n[d]: #details\n"
	referenceDocHref   = "/ui/doc?uri=acdc%3A%2F%2Fguides%2Fsetup#details"
)

func renderWithReferences(chunk string, references render.References, headingIDs bool, resolver *fakeResolver) string {
	return render.Markdown(chunk, render.Options{
		FromPath:   fromPath,
		SourceURI:  referenceSourceURI,
		HeadingIDs: headingIDs,
		References: references,
		Links:      resolver,
	})
}

func renderChunkOnly(chunk string) string {
	return render.Markdown(chunk, render.Options{
		FromPath: fromPath, SourceURI: referenceSourceURI, Links: newFakeResolver(nil),
	})
}

func TestMarkdown_CollectedReferenceResolvesChunkReference(t *testing.T) {
	cases := []struct {
		name       string
		headingIDs bool
		href       string
	}{
		{name: "preview qualifies fragment with source document", headingIDs: false, href: referenceDocHref},
		{name: "whole document keeps same-page fragment", headingIDs: true, href: "#details"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := newFakeResolver(nil)
			rendered := renderWithReferences(referenceChunk, render.CollectReferences(referenceDocument), tc.headingIDs, resolver)

			a := singleAnchor(t, rendered)
			requireHref(t, a, tc.href)
			requireInternal(t, a)
			require.Equal(t, "Details", a.inner)
			require.NotContains(t, rendered, "Section body text", "collected references contribute definitions only, never content")
			require.NotContains(t, rendered, "[d]")
			require.Empty(t, resolver.recordedCalls())
		})
	}
}

func TestMarkdown_CollectedReferencesFirstDefinitionInDocumentOrderWins(t *testing.T) {
	const chunk = "# Later\n\nSee [Details][d].\n\n[d]: #second\n"
	const document = "# Early\n\n[d]: #first\n\n" + chunk

	rendered := renderWithReferences(chunk, render.CollectReferences(document), false, newFakeResolver(nil))

	requireHref(t, singleAnchor(t, rendered), "/ui/doc?uri=acdc%3A%2F%2Fguides%2Fsetup#first")
}

func TestMarkdown_CollectedReferencesDuplicateDefinitionsKeepFirst(t *testing.T) {
	const document = "# A\n\n[d]: #alpha\n\n# B\n\n[d]: #beta\n"

	rendered := renderWithReferences(referenceChunk, render.CollectReferences(document), false, newFakeResolver(nil))

	requireHref(t, singleAnchor(t, rendered), "/ui/doc?uri=acdc%3A%2F%2Fguides%2Fsetup#alpha")
}

func TestMarkdown_ChunkOnlyDefinitionStillResolvesWithCollectedReferences(t *testing.T) {
	const chunk = "See [Install][i].\n\n[i]: #install\n"
	const document = "# Unrelated\n\n[other]: #elsewhere\n"

	rendered := renderWithReferences(chunk, render.CollectReferences(document), false, newFakeResolver(nil))

	requireHref(t, singleAnchor(t, rendered), "/ui/doc?uri=acdc%3A%2F%2Fguides%2Fsetup#install")
}

func TestMarkdown_ZeroReferencesRendersChunkOnly(t *testing.T) {
	chunks := []struct {
		name  string
		chunk string
	}{
		{name: "undefined reference stays literal", chunk: referenceChunk},
		{name: "chunk definition resolves", chunk: "See [Install][i].\n\n[i]: #install\n"},
	}
	references := []struct {
		name       string
		references render.References
	}{
		{name: "zero value", references: render.References{}},
		{name: "collected from empty source", references: render.CollectReferences("")},
	}

	for _, c := range chunks {
		for _, r := range references {
			t.Run(c.name+"/"+r.name, func(t *testing.T) {
				rendered := renderWithReferences(c.chunk, r.references, false, newFakeResolver(nil))

				require.Equal(t, renderChunkOnly(c.chunk), rendered)
			})
		}
	}

	rendered := renderWithReferences(referenceChunk, render.References{}, false, newFakeResolver(nil))
	require.Empty(t, anchors(rendered))
	require.Contains(t, rendered, "[Details][d]")
}

func TestMarkdown_CollectedReferencesUnsafeDefinitionRendersBroken(t *testing.T) {
	const document = "# Intro\n\nSee [Details][d].\n\n[d]: javascript:alert(1)\n"

	rendered := renderWithReferences(referenceChunk, render.CollectReferences(document), false, newFakeResolver(nil))

	require.Empty(t, anchors(rendered))
	broken := brokenPattern.FindStringSubmatch(rendered)
	require.NotNil(t, broken, "expected a broken-link span in %s", rendered)
	require.Equal(t, "Broken link: javascript:alert(1)", broken[1])
	require.Equal(t, "Details", broken[2])
	require.NotContains(t, rendered, "href=", "unsafe target must not reach an href")
}

func TestMarkdown_CollectedReferencesReusedAcrossRendersYieldIdenticalOutput(t *testing.T) {
	references := render.CollectReferences(referenceDocument)

	first := renderWithReferences(referenceChunk, references, false, newFakeResolver(nil))
	for range 5 {
		require.Equal(t, first, renderWithReferences(referenceChunk, references, false, newFakeResolver(nil)))
	}
	requireHref(t, singleAnchor(t, first), referenceDocHref)
}

func TestMarkdown_CollectedReferencesIsolateChunkLocalDefinitions(t *testing.T) {
	references := render.CollectReferences(referenceDocument)
	const definingChunk = "See [Local][x].\n\n[x]: #local\n"
	const usingChunk = "See [Local][x]."

	defining := renderWithReferences(definingChunk, references, false, newFakeResolver(nil))
	using := renderWithReferences(usingChunk, references, false, newFakeResolver(nil))

	requireHref(t, singleAnchor(t, defining), "/ui/doc?uri=acdc%3A%2F%2Fguides%2Fsetup#local")
	require.Empty(t, anchors(using), "a chunk-local definition must not leak into later renders sharing the References")
	require.Contains(t, using, "[Local][x]")
}

func TestMarkdown_CollectedReferencesSafeForConcurrentReuse(t *testing.T) {
	references := render.CollectReferences(referenceDocument)
	chunks := []string{referenceChunk, "See [Local][x].\n\n[x]: #local\n", "See [Local][x]."}
	want := make([]string, len(chunks))
	for i, chunk := range chunks {
		want[i] = renderWithReferences(chunk, references, false, newFakeResolver(nil))
	}

	const goroutines = 16
	got := make([][]string, goroutines)
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Go(func() {
			got[g] = make([]string, 0, len(chunks)*10)
			for range 10 {
				for _, chunk := range chunks {
					got[g] = append(got[g], renderWithReferences(chunk, references, false, newFakeResolver(nil)))
				}
			}
		})
	}
	wg.Wait()

	for g := range goroutines {
		for i, rendered := range got[g] {
			require.Equal(t, want[i%len(chunks)], rendered, "goroutine %d render %d", g, i)
		}
	}
}

func BenchmarkMarkdown_ManyResultsFromOneSourceWithCollectedReferences(b *testing.B) {
	const results = 100
	var document strings.Builder
	chunks := make([]string, results)
	for i := range results {
		chunks[i] = fmt.Sprintf("## Section %d\n\nNeedle %d, see [Details][d].\n", i, i)
		document.WriteString(chunks[i])
		document.WriteString("\n")
	}
	document.WriteString("| a | b |\n|---|---|\n")
	for i := range 5000 {
		fmt.Fprintf(&document, "| row %d | value %d |\n", i, i)
	}
	document.WriteString("\n[d]: #details\n")
	source := document.String()

	b.ReportAllocs()
	for b.Loop() {
		references := render.CollectReferences(source)
		for _, chunk := range chunks {
			renderWithReferences(chunk, references, false, newFakeResolver(nil))
		}
	}
}
