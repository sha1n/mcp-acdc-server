package content

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProjectPlainText_RemovesMarkdownSyntax(t *testing.T) {
	tests := []struct {
		name     string
		markdown string
		want     string
	}{
		{name: "empty", markdown: "", want: ""},
		{name: "plain paragraph", markdown: "Just text.", want: "Just text."},
		{name: "atx headings", markdown: "# Title\n\n## Sub Title ##\n\nBody.", want: "Title — Sub Title — Body."},
		{name: "setext heading", markdown: "Title\n=====\n\nBody.", want: "Title — Body."},
		{name: "emphasis", markdown: "Use **bold**, _italic_, *star* and __strong__ text.", want: "Use bold, italic, star and strong text."},
		{name: "strikethrough", markdown: "Was ~~old~~ new.", want: "Was old new."},
		{name: "inline code", markdown: "Run `make lint` now.", want: "Run make lint now."},
		{name: "double backtick code", markdown: "Type ``a ` b`` here.", want: "Type a ` b here."},
		{name: "inline link", markdown: "See [the guide](https://example.com/guide \"Guide\") first.", want: "See the guide first."},
		{name: "reference link", markdown: "See [the guide][g] first.\n\n[g]: https://example.com/guide", want: "See the guide first."},
		{name: "image keeps alt text", markdown: "Logo ![the logo](logo.png) here.", want: "Logo the logo here."},
		{name: "angle autolink", markdown: "Visit <https://example.com/docs> today.", want: "Visit https://example.com/docs today."},
		{name: "bare gfm autolink", markdown: "Visit https://example.com/docs today.", want: "Visit https://example.com/docs today."},
		{name: "table", markdown: "| Field | Boost |\n|---|:---:|\n| title | high |\n| content | low |", want: "Field · Boost / title · high / content · low"},
		{name: "backtick fence drops info string", markdown: "Example:\n\n```yaml\nname: demo\nkind: widget\n```\n\nAfter.", want: "Example: — name: demo kind: widget — After."},
		{name: "tilde fence", markdown: "~~~\nrun widget\n~~~", want: "run widget"},
		{name: "indented code", markdown: "Code:\n\n    go test ./...\n\nDone.", want: "Code: — go test ./... — Done."},
		{name: "soft line break", markdown: "first line\nsecond line", want: "first line second line"},
		{name: "hard line break with spaces", markdown: "first line  \nsecond line", want: "first line second line"},
		{name: "hard line break with backslash", markdown: "first line\\\nsecond line", want: "first line second line"},
		{name: "blockquote", markdown: "> quoted **text**\n> continues", want: "quoted text continues"},
		{name: "lists", markdown: "- one\n- two\n\n1. three\n2. four", want: "one — two — three — four"},
		{name: "task list markers", markdown: "- [ ] todo item\n- [x] done item", want: "todo item — done item"},
		{name: "thematic break", markdown: "above\n\n---\n\nbelow", want: "above — below"},
		{name: "html block kept literal", markdown: "<div class=\"note\">\nInside\n</div>\n\nAfter.", want: "<div class=\"note\"> Inside </div> — After."},
		{name: "html comment block keeps closing line", markdown: "<!--\ncomment\n-->\n\nAfter.", want: "<!-- comment --> — After."},
		{name: "html script block keeps closing line", markdown: "<script>\nx = 1\n</script>\n\nAfter.", want: "<script> x = 1 </script> — After."},
		{name: "raw inline html kept literal", markdown: "Literal <mark>x</mark> tag.", want: "Literal <mark>x</mark> tag."},
		{name: "multibyte text", markdown: "## Café\n\nnaïve **façade**", want: "Café — naïve façade"},
		{name: "backslash escape decoded", markdown: "Not \\*emphasis\\* here.", want: "Not *emphasis* here."},
		{name: "escaped backslash decoded", markdown: "A \\\\ slash.", want: "A \\ slash."},
		{name: "named entity decoded", markdown: "AT&amp;T and &lt;tag&gt;", want: "AT&T and <tag>"},
		{name: "unknown named entity kept literal", markdown: "a &bogus; b", want: "a &bogus; b"},
		{name: "malformed reference kept literal", markdown: "a &x-y; b", want: "a &x-y; b"},
		{name: "unterminated entity kept literal", markdown: "AT&amp", want: "AT&amp"},
		{name: "decimal character reference decoded", markdown: "caf&#233;", want: "café"},
		{name: "hex character reference decoded", markdown: "caf&#xE9;", want: "café"},
		{name: "escape inside link text decoded", markdown: "[a \\[b\\]](https://example.com)", want: "a [b]"},
		{name: "entity inside table cell decoded", markdown: "| a |\n|---|\n| x &amp; y |", want: "a / x & y"},
		{name: "paragraphs", markdown: "One.\n\nTwo.\n\nThree.", want: "One. — Two. — Three."},
		{name: "heading then paragraph", markdown: "# Title\nBody text\ncontinues.", want: "Title — Body text continues."},
		{name: "blockquote paragraphs", markdown: "> first\n>\n> second", want: "first — second"},
		{name: "blockquote between paragraphs", markdown: "before\n\n> quoted\n\nafter", want: "before — quoted — after"},
		{name: "nested blockquote", markdown: "> outer\n> > inner", want: "outer — inner"},
		{name: "loose list item paragraphs", markdown: "- a\n\n  b\n- c", want: "a — b — c"},
		{name: "nested list", markdown: "- parent\n  - child\n- sibling", want: "parent — child — sibling"},
		{name: "list after paragraph", markdown: "Intro:\n\n- one\n- two", want: "Intro: — one — two"},
		{name: "code block lines join with spaces", markdown: "```\nline one\nline two\n```", want: "line one line two"},
		{name: "table header only", markdown: "| a | b |\n|---|---|", want: "a · b"},
		{name: "single column table", markdown: "| a |\n|---|\n| b |\n| c |", want: "a / b / c"},
		{name: "table cell inline content", markdown: "| **bold** `code` | [link](https://x.io) text |\n|---|---|\n| one two | three |", want: "bold code · link text / one two · three"},
		{name: "table between blocks", markdown: "# Fields\n\n| a | b |\n|---|---|\n| c | d |\n\nAfter.", want: "Fields — a · b / c · d — After."},
		{name: "mixed blocks", markdown: "# Guide\n\nIntro.\n\n- step one\n- step two\n\n```sh\nmake test\n```\n\n> Note.", want: "Guide — Intro. — step one — step two — make test — Note."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ProjectPlainText(tt.markdown).Text)
		})
	}
}

func TestProjectPlainText_NeverStartsOrEndsWithSeparator(t *testing.T) {
	inputs := []string{
		"\n\n# Title\n\n",
		"---\n\ntext\n\n---",
		"- \n- item\n- ",
		"#\n\ntext\n\n#",
		">\n\ntext\n\n>",
		"```\n```\n\ntext\n\n```\n```",
		"| a | |\n|---|---|\n| | d |\n|  |  |",
		"| |\n|---|\n| x |",
		"- \n  - \n- x",
		"> > \n> text",
	}
	for _, markdown := range inputs {
		text := ProjectPlainText(markdown).Text
		require.Equal(t, strings.TrimSpace(text), text, "projection of %q", markdown)
		require.NotContains(t, text, "  ", "projection of %q", markdown)
		for _, separator := range []string{"—", "·", "/"} {
			require.False(t, strings.HasPrefix(text, separator), "projection of %q starts with %q: %q", markdown, separator, text)
			require.False(t, strings.HasSuffix(text, separator), "projection of %q ends with %q: %q", markdown, separator, text)
		}
		fields := strings.Fields(text)
		for i := 1; i < len(fields); i++ {
			require.False(t, isSeparatorField(fields[i-1]) && isSeparatorField(fields[i]), "projection of %q doubles separators: %q", markdown, text)
		}
	}
}

func isSeparatorField(field string) bool {
	return field == "—" || field == "·" || field == "/"
}

func TestPlainText_LocateNeverMapsIntoSeparators(t *testing.T) {
	tests := []struct {
		name     string
		markdown string
		source   string
		want     string
		wantOK   bool
	}{
		{name: "gap between paragraphs", markdown: "One.\n\nTwo.", source: "\n\n", wantOK: false},
		{name: "gap between heading and paragraph", markdown: "# Title\n\nBody.", source: "\n\n", wantOK: false},
		{name: "pipe between cells", markdown: "| a | b |\n|---|---|\n| c | d |", source: " | ", wantOK: false},
		{name: "row boundary", markdown: "| a | b |\n|---|---|\n| c | d |", source: "|\n|", wantOK: false},
		{name: "list item boundary", markdown: "- one\n- two", source: "\n- ", wantOK: false},
		{name: "word before block separator", markdown: "One.\n\nTwo.", source: "One.", want: "One.", wantOK: true},
		{name: "word after block separator", markdown: "One.\n\nTwo.", source: "Two.", want: "Two.", wantOK: true},
		{name: "cell before cell separator", markdown: "| a | b |\n|---|---|\n| c | d |", source: "c", want: "c", wantOK: true},
		{name: "cell after row separator", markdown: "| a | b |\n|---|---|\n| c | d |", source: "d", want: "d", wantOK: true},
		{name: "second list item", markdown: "- one\n- two", source: "two", want: "two", wantOK: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := nthIndex(t, tt.markdown, tt.source, 1)
			projection := ProjectPlainText(tt.markdown)

			from, to, ok := projection.Locate(start, start+len(tt.source))

			require.Equal(t, tt.wantOK, ok, "locating %q in projection %q", tt.source, projection.Text)
			if tt.wantOK {
				require.Equal(t, tt.want, projection.Text[from:to])
			}
		})
	}
}

func TestPlainText_Locate(t *testing.T) {
	tests := []struct {
		name       string
		markdown   string
		source     string
		occurrence int
		want       string
		wantOK     bool
	}{
		{name: "plain word", markdown: "Just some text.", source: "some", want: "some", wantOK: true},
		{name: "heading text", markdown: "# Search Features\n\nBody.", source: "Features", want: "Features", wantOK: true},
		{name: "emphasised word", markdown: "Use **bold** text.", source: "bold", want: "bold", wantOK: true},
		{name: "inline code text", markdown: "Run `make lint` now.", source: "lint", want: "lint", wantOK: true},
		{name: "link text", markdown: "See [the guide](https://example.com/guide).", source: "guide", want: "guide", wantOK: true},
		{name: "table cell", markdown: "| Field | Boost |\n|---|---|\n| title | high |", source: "high", want: "high", wantOK: true},
		{name: "code fence line", markdown: "```yaml\nname: demo\n```", source: "demo", want: "demo", wantOK: true},
		{name: "raw inline html", markdown: "Literal <mark>x</mark> tag.", source: "<mark>", want: "<mark>", wantOK: true},
		{name: "multibyte word after removed syntax", markdown: "## Café\n\n**façade** here", source: "façade", want: "façade", wantOK: true},
		{name: "second occurrence", markdown: "word **word** word", source: "word", occurrence: 2, want: "word", wantOK: true},
		{name: "word after soft break", markdown: "first\nsecond", source: "second", want: "second", wantOK: true},
		{name: "link url", markdown: "See [the guide](https://example.com/guide).", source: "example", wantOK: false},
		{name: "link destination parentheses", markdown: "See [the guide](https://example.com/guide).", source: "](", wantOK: false},
		{name: "heading marker", markdown: "# Title", source: "#", wantOK: false},
		{name: "emphasis marker", markdown: "Use **bold** text.", source: "**", wantOK: false},
		{name: "backtick", markdown: "Run `make lint` now.", source: "`", wantOK: false},
		{name: "table pipe", markdown: "| Field | Boost |\n|---|---|\n| title | high |", source: "|", wantOK: false},
		{name: "table delimiter row", markdown: "| Field | Boost |\n|---|---|\n| title | high |", source: "---", wantOK: false},
		{name: "code fence info string", markdown: "```yaml\nname: demo\n```", source: "yaml", wantOK: false},
		{name: "task list marker", markdown: "- [x] done item", source: "[x]", wantOK: false},
		{name: "range spanning into removed syntax", markdown: "Use **bold** text.", source: "bold**", wantOK: false},
		{name: "backslash escape", markdown: "Not \\*emphasis\\* here.", source: "\\*", want: "*", wantOK: true},
		{name: "escaped word", markdown: "Not \\*emphasis\\* here.", source: "\\*emphasis\\*", want: "*emphasis*", wantOK: true},
		{name: "word after escape", markdown: "Not \\*emphasis\\* here.", source: "here", want: "here", wantOK: true},
		{name: "named entity", markdown: "AT&amp;T rocks", source: "&amp;", want: "&", wantOK: true},
		{name: "word spanning an entity", markdown: "AT&amp;T rocks", source: "AT&amp;T", want: "AT&T", wantOK: true},
		{name: "word after entity", markdown: "AT&amp;T rocks", source: "rocks", want: "rocks", wantOK: true},
		{name: "character reference", markdown: "caf&#233; open", source: "caf&#233;", want: "café", wantOK: true},
		{name: "word after character reference", markdown: "caf&#233; open", source: "open", want: "open", wantOK: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := nthIndex(t, tt.markdown, tt.source, tt.occurrence)
			projection := ProjectPlainText(tt.markdown)

			from, to, ok := projection.Locate(start, start+len(tt.source))

			require.Equal(t, tt.wantOK, ok, "locating %q in projection %q", tt.source, projection.Text)
			if tt.wantOK {
				require.Equal(t, tt.want, projection.Text[from:to])
			}
		})
	}
}

func TestPlainText_LocateRejectsEmptyAndOutOfRangeSpans(t *testing.T) {
	projection := ProjectPlainText("Just some text.")

	tests := []struct {
		name       string
		start, end int
	}{
		{name: "empty", start: 5, end: 5},
		{name: "reversed", start: 9, end: 5},
		{name: "negative", start: -3, end: -1},
		{name: "past the end", start: 100, end: 104},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, ok := projection.Locate(tt.start, tt.end)
			require.False(t, ok)
		})
	}
}

func nthIndex(t *testing.T, text, substring string, occurrence int) int {
	t.Helper()
	if occurrence == 0 {
		occurrence = 1
	}
	offset := 0
	for i := 1; ; i++ {
		index := strings.Index(text[offset:], substring)
		require.GreaterOrEqual(t, index, 0, "occurrence %d of %q not in %q", occurrence, substring, text)
		if i == occurrence {
			return offset + index
		}
		offset += index + len(substring)
	}
}
