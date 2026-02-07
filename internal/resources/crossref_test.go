package resources

import (
	"path/filepath"
	"testing"
)

func TestCrossRefTransformer_BasicRelativeLink(t *testing.T) {
	defs := []ResourceDefinition{
		{URI: "acdc://other", FilePath: "/content/resources/other.md"},
	}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "See [other doc](other.md) for details."
	got := transformer(input, current)
	want := "See [other doc](acdc://other) for details."

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCrossRefTransformer_ParentDirectoryLink(t *testing.T) {
	defs := []ResourceDefinition{
		{URI: "acdc://guides/intro", FilePath: "/content/resources/guides/intro.md"},
	}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/tutorials/setup.md"}
	input := "See [intro](../guides/intro.md) first."
	got := transformer(input, current)
	want := "See [intro](acdc://guides/intro) first."

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCrossRefTransformer_DotSlashLink(t *testing.T) {
	defs := []ResourceDefinition{
		{URI: "acdc://sibling", FilePath: "/content/resources/sibling.md"},
	}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "[sibling](./sibling.md)"
	got := transformer(input, current)
	want := "[sibling](acdc://sibling)"

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCrossRefTransformer_SubdirectoryLink(t *testing.T) {
	defs := []ResourceDefinition{
		{URI: "acdc://sub/deep", FilePath: "/content/resources/sub/deep.md"},
	}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "[deep](sub/deep.md)"
	got := transformer(input, current)
	want := "[deep](acdc://sub/deep)"

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCrossRefTransformer_LinkWithFragment(t *testing.T) {
	defs := []ResourceDefinition{
		{URI: "acdc://guide", FilePath: "/content/resources/guide.md"},
	}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "See [setup section](guide.md#setup) for details."
	got := transformer(input, current)
	want := "See [setup section](acdc://guide#setup) for details."

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCrossRefTransformer_LinkWithTitle(t *testing.T) {
	defs := []ResourceDefinition{
		{URI: "acdc://guide", FilePath: "/content/resources/guide.md"},
	}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := `See [guide](guide.md "The Guide") for help.`
	got := transformer(input, current)
	want := `See [guide](acdc://guide "The Guide") for help.`

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCrossRefTransformer_LinkWithFragmentAndTitle(t *testing.T) {
	defs := []ResourceDefinition{
		{URI: "acdc://guide", FilePath: "/content/resources/guide.md"},
	}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := `[guide](guide.md#intro "The Guide")`
	got := transformer(input, current)
	want := `[guide](acdc://guide#intro "The Guide")`

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCrossRefTransformer_AbsoluteURLUnchanged(t *testing.T) {
	defs := []ResourceDefinition{}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "Visit [example](https://example.com) for more."
	got := transformer(input, current)

	if got != input {
		t.Errorf("got %q, want %q (unchanged)", got, input)
	}
}

func TestCrossRefTransformer_HttpURLUnchanged(t *testing.T) {
	defs := []ResourceDefinition{}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "Visit [example](http://example.com) for more."
	got := transformer(input, current)

	if got != input {
		t.Errorf("got %q, want %q (unchanged)", got, input)
	}
}

func TestCrossRefTransformer_DefaultSchemeURIUnchanged(t *testing.T) {
	defs := []ResourceDefinition{}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "See [guide](acdc://guide) for details."
	got := transformer(input, current)

	if got != input {
		t.Errorf("got %q, want %q (unchanged)", got, input)
	}
}

func TestCrossRefTransformer_CustomSchemeURIUnchanged(t *testing.T) {
	defs := []ResourceDefinition{}
	transformer := NewCrossRefTransformer(defs, "myco")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "See [guide](myco://guide) for details."
	got := transformer(input, current)

	if got != input {
		t.Errorf("got %q, want %q (unchanged)", got, input)
	}
}

func TestCrossRefTransformer_FragmentOnlyUnchanged(t *testing.T) {
	defs := []ResourceDefinition{}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "See [section](#setup) below."
	got := transformer(input, current)

	if got != input {
		t.Errorf("got %q, want %q (unchanged)", got, input)
	}
}

func TestCrossRefTransformer_ImageLinkUnchanged(t *testing.T) {
	defs := []ResourceDefinition{
		{URI: "acdc://image", FilePath: "/content/resources/image.md"},
	}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "![alt text](image.png)"
	got := transformer(input, current)

	if got != input {
		t.Errorf("got %q, want %q (unchanged)", got, input)
	}
}

func TestCrossRefTransformer_UnknownFileUnchanged(t *testing.T) {
	defs := []ResourceDefinition{
		{URI: "acdc://known", FilePath: "/content/resources/known.md"},
	}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "See [unknown](nonexistent.md) file."
	got := transformer(input, current)

	if got != input {
		t.Errorf("got %q, want %q (unchanged)", got, input)
	}
}

func TestCrossRefTransformer_MailtoUnchanged(t *testing.T) {
	defs := []ResourceDefinition{}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "Contact [us](mailto:test@example.com) for help."
	got := transformer(input, current)

	if got != input {
		t.Errorf("got %q, want %q (unchanged)", got, input)
	}
}

func TestCrossRefTransformer_MultipleLinks(t *testing.T) {
	defs := []ResourceDefinition{
		{URI: "acdc://guide-a", FilePath: "/content/resources/guide-a.md"},
		{URI: "acdc://guide-b", FilePath: "/content/resources/guide-b.md"},
	}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "Read [A](guide-a.md) and [B](guide-b.md) first."
	got := transformer(input, current)
	want := "Read [A](acdc://guide-a) and [B](acdc://guide-b) first."

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCrossRefTransformer_MixedLinks(t *testing.T) {
	defs := []ResourceDefinition{
		{URI: "acdc://guide", FilePath: "/content/resources/guide.md"},
	}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "See [guide](guide.md), [ext](https://example.com), and [section](#foo)."
	got := transformer(input, current)
	want := "See [guide](acdc://guide), [ext](https://example.com), and [section](#foo)."

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCrossRefTransformer_NoLinks(t *testing.T) {
	defs := []ResourceDefinition{
		{URI: "acdc://guide", FilePath: "/content/resources/guide.md"},
	}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "Just plain text with no links."
	got := transformer(input, current)

	if got != input {
		t.Errorf("got %q, want %q (unchanged)", got, input)
	}
}

func TestCrossRefTransformer_EmptyContent(t *testing.T) {
	defs := []ResourceDefinition{}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	got := transformer("", current)

	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestCrossRefTransformer_LinkAtStartOfLine(t *testing.T) {
	defs := []ResourceDefinition{
		{URI: "acdc://guide", FilePath: "/content/resources/guide.md"},
	}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "[guide](guide.md)"
	got := transformer(input, current)
	want := "[guide](acdc://guide)"

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCrossRefTransformer_LinkAtStartOfMultiline(t *testing.T) {
	defs := []ResourceDefinition{
		{URI: "acdc://guide", FilePath: "/content/resources/guide.md"},
	}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "First line\n[guide](guide.md)\nLast line"
	got := transformer(input, current)
	want := "First line\n[guide](acdc://guide)\nLast line"

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCrossRefTransformer_CustomSchemeTransformation(t *testing.T) {
	defs := []ResourceDefinition{
		{URI: "myco://docs/intro", FilePath: "/content/resources/docs/intro.md"},
	}
	transformer := NewCrossRefTransformer(defs, "myco")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "See [intro](docs/intro.md) to start."
	got := transformer(input, current)
	want := "See [intro](myco://docs/intro) to start."

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCrossRefTransformer_EmptyLinkText(t *testing.T) {
	defs := []ResourceDefinition{
		{URI: "acdc://guide", FilePath: "/content/resources/guide.md"},
	}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "[](guide.md)"
	got := transformer(input, current)
	want := "[](acdc://guide)"

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCrossRefTransformer_FtpSchemeUnchanged(t *testing.T) {
	defs := []ResourceDefinition{}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "Get [file](ftp://server/file) here."
	got := transformer(input, current)

	if got != input {
		t.Errorf("got %q, want %q (unchanged)", got, input)
	}
}

func TestCrossRefTransformer_UsesAbsoluteFilePathResolution(t *testing.T) {
	// Ensure the transformer correctly uses filepath.Clean to resolve paths
	absPath := filepath.Clean("/content/resources/deep/nested/doc.md")
	defs := []ResourceDefinition{
		{URI: "acdc://deep/nested/doc", FilePath: absPath},
	}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/other/current.md"}
	input := "[doc](../deep/nested/doc.md)"
	got := transformer(input, current)
	want := "[doc](acdc://deep/nested/doc)"

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCrossRefTransformer_EmptyDefinitions(t *testing.T) {
	transformer := NewCrossRefTransformer(nil, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "See [link](other.md) for more."
	got := transformer(input, current)

	// No definitions to match, so link stays unchanged
	if got != input {
		t.Errorf("got %q, want %q (unchanged)", got, input)
	}
}

func TestCrossRefTransformer_ConsecutiveLinksOnSameLine(t *testing.T) {
	defs := []ResourceDefinition{
		{URI: "acdc://a", FilePath: "/content/resources/a.md"},
		{URI: "acdc://b", FilePath: "/content/resources/b.md"},
	}
	transformer := NewCrossRefTransformer(defs, "acdc")

	current := ResourceDefinition{FilePath: "/content/resources/current.md"}
	input := "[a](a.md)[b](b.md)"
	got := transformer(input, current)
	want := "[a](acdc://a)[b](acdc://b)"

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
