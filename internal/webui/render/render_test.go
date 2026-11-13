package render_test

import (
	"html"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/webui/render"
	"github.com/stretchr/testify/require"
)

const fromPath = "guides/setup.md"

// resolveCall records a ResolveURI call with an empty fromPath, so expected
// calls also pin which resolver method a target reaches.
type resolveCall struct {
	fromPath string
	target   string
}

type fakeResolver struct {
	mu      sync.Mutex
	targets map[string]string
	calls   []resolveCall
}

func newFakeResolver(targets map[string]string) *fakeResolver {
	return &fakeResolver{targets: targets}
}

func (f *fakeResolver) ResolvePath(fromPath, relPath string) (string, bool) {
	return f.record(resolveCall{fromPath: fromPath, target: relPath})
}

func (f *fakeResolver) ResolveURI(uri string) (string, bool) {
	return f.record(resolveCall{target: uri})
}

func (f *fakeResolver) record(call resolveCall) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	uri, ok := f.targets[call.target]
	return uri, ok
}

func (f *fakeResolver) recordedCalls() []resolveCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]resolveCall(nil), f.calls...)
}

type anchor struct {
	attrs string
	inner string
}

var (
	anchorPattern     = regexp.MustCompile(`(?s)<a\b([^>]*)>(.*?)</a>`)
	brokenPattern     = regexp.MustCompile(`(?s)<span class="broken-link" title="([^"]*)">(.*?)</span>`)
	imagePattern      = regexp.MustCompile(`(?s)<span class="md-image" title="([^"]*)">(.*?)</span>`)
	headingIDPattern  = regexp.MustCompile(`<h[1-6]\b[^>]*\bid="([^"]*)"`)
	headingTagPattern = regexp.MustCompile(`<h[1-6]\b[^>]*>`)
)

func renderMarkdown(t *testing.T, source string, resolver *fakeResolver, headingIDs bool) string {
	t.Helper()
	out := render.Markdown(source, render.Options{FromPath: fromPath, HeadingIDs: headingIDs, Links: resolver})
	require.NotEmpty(t, out, "rendered output for %q", source)
	return out
}

func anchors(rendered string) []anchor {
	var found []anchor
	for _, match := range anchorPattern.FindAllStringSubmatch(rendered, -1) {
		found = append(found, anchor{attrs: match[1], inner: match[2]})
	}
	return found
}

func singleAnchor(t *testing.T, rendered string) anchor {
	t.Helper()
	found := anchors(rendered)
	require.Len(t, found, 1, "expected exactly one anchor in %s", rendered)
	return found[0]
}

func attr(attrs, name string) (string, bool) {
	match := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `="([^"]*)"`).FindStringSubmatch(attrs)
	if match == nil {
		return "", false
	}
	return html.UnescapeString(match[1]), true
}

func requireHref(t *testing.T, a anchor, want string) {
	t.Helper()
	href, ok := attr(a.attrs, "href")
	require.True(t, ok, "anchor without href: %q", a.attrs)
	require.Equal(t, want, href)
}

func requireInternal(t *testing.T, a anchor) {
	t.Helper()
	_, hasTarget := attr(a.attrs, "target")
	require.False(t, hasTarget, "internal link must not open a new tab: %q", a.attrs)
}

func TestMarkdown_SameDocumentFragmentLinkUnchanged(t *testing.T) {
	cases := []struct {
		name       string
		headingIDs bool
		href       string
	}{
		{name: "whole document keeps in-page link", headingIDs: true, href: "#install-1"},
		{name: "preview qualifies with source document", headingIDs: false, href: "/ui/doc?uri=acdc%3A%2F%2Fguides%2Fsetup#install-1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := newFakeResolver(nil)
			rendered := render.Markdown("[jump](#install-1)", render.Options{
				FromPath: fromPath, SourceURI: "acdc://guides/setup", HeadingIDs: tc.headingIDs, Links: resolver,
			})

			a := singleAnchor(t, rendered)
			requireHref(t, a, tc.href)
			requireInternal(t, a)
			require.Equal(t, "jump", a.inner)
			require.Empty(t, resolver.recordedCalls())
		})
	}
}

func TestMarkdown_ExternalLinksOpenInNewTabWithoutOpener(t *testing.T) {
	cases := []struct {
		name     string
		markdown string
		href     string
		inner    string
	}{
		{name: "https inline", markdown: "[site](https://example.com/a?b=1&c=2#frag)", href: "https://example.com/a?b=1&c=2#frag", inner: "site"},
		{name: "http inline", markdown: "[site](http://example.com/x)", href: "http://example.com/x", inner: "site"},
		{name: "uppercase scheme", markdown: "[site](HTTPS://EXAMPLE.COM/X)", href: "HTTPS://EXAMPLE.COM/X", inner: "site"},
		{name: "angle autolink", markdown: "<https://example.com/auto>", href: "https://example.com/auto", inner: "https://example.com/auto"},
		{name: "bare url autolink", markdown: "see https://example.org/bare here", href: "https://example.org/bare", inner: "https://example.org/bare"},
		{name: "www autolink", markdown: "see www.example.com here", href: "http://www.example.com", inner: "www.example.com"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := newFakeResolver(nil)
			rendered := renderMarkdown(t, tc.markdown, resolver, false)

			a := singleAnchor(t, rendered)
			requireHref(t, a, tc.href)
			target, _ := attr(a.attrs, "target")
			rel, _ := attr(a.attrs, "rel")
			require.Equal(t, "_blank", target)
			require.Equal(t, "noopener noreferrer", rel)
			require.Equal(t, tc.inner, a.inner)
			require.Empty(t, resolver.recordedCalls())
			require.NotContains(t, rendered, "broken-link")
		})
	}
}

func TestMarkdown_EmailLinksRenderAsPlainText(t *testing.T) {
	cases := []struct {
		name     string
		markdown string
		text     string
	}{
		{name: "mailto inline", markdown: "write to [the team](mailto:team@example.com) today", text: "write to the team today"},
		{name: "mailto uppercase", markdown: "[Team](MAILTO:team@example.com)", text: "Team"},
		{name: "email angle autolink", markdown: "mail <team@example.com> now", text: "mail team@example.com now"},
		{name: "bare email autolink", markdown: "mail team@example.com now", text: "mail team@example.com now"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := newFakeResolver(nil)
			rendered := renderMarkdown(t, tc.markdown, resolver, false)

			require.Empty(t, anchors(rendered), "email links are not navigable: %s", rendered)
			require.NotContains(t, rendered, "broken-link")
			require.NotContains(t, rendered, "mailto:")
			require.Contains(t, rendered, tc.text)
			require.Empty(t, resolver.recordedCalls())
		})
	}
}

func TestMarkdown_ResolvedLinksPointAtDocumentURL(t *testing.T) {
	cases := []struct {
		name     string
		markdown string
		call     resolveCall
		href     string
		inner    string
	}{
		{
			name:     "scheme uri keeps authored fragment",
			markdown: "[other](acdc://docs/other#install)",
			call:     resolveCall{target: "acdc://docs/other"},
			href:     "/ui/doc?uri=acdc%3A%2F%2Fdocs%2Fother#install",
			inner:    "other",
		},
		{
			name:     "scheme uri autolink",
			markdown: "<acdc://docs/other>",
			call:     resolveCall{target: "acdc://docs/other"},
			href:     "/ui/doc?uri=acdc%3A%2F%2Fdocs%2Fother",
			inner:    "acdc://docs/other",
		},
		{
			name:     "relative path is percent-decoded and fragment stripped",
			markdown: "[ref](../ref/other%20file.md#sec)",
			call:     resolveCall{fromPath: fromPath, target: "../ref/other file.md"},
			href:     "/ui/doc?uri=acdc%3A%2F%2Fref%2Fother-file#sec",
			inner:    "ref",
		},
		{
			name:     "sibling path without fragment",
			markdown: "[sib](sibling.md)",
			call:     resolveCall{fromPath: fromPath, target: "sibling.md"},
			href:     "/ui/doc?uri=acdc%3A%2F%2Fguides%2Fsibling",
			inner:    "sib",
		},
		{
			name:     "children markup is kept",
			markdown: "[**bold** text](sibling.md)",
			call:     resolveCall{fromPath: fromPath, target: "sibling.md"},
			href:     "/ui/doc?uri=acdc%3A%2F%2Fguides%2Fsibling",
			inner:    "<strong>bold</strong> text",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := newFakeResolver(map[string]string{
				"acdc://docs/other":    "acdc://docs/other",
				"../ref/other file.md": "acdc://ref/other-file",
				"sibling.md":           "acdc://guides/sibling",
			})
			rendered := renderMarkdown(t, tc.markdown, resolver, false)

			a := singleAnchor(t, rendered)
			requireHref(t, a, tc.href)
			requireInternal(t, a)
			require.Equal(t, tc.inner, a.inner)
			require.Equal(t, []resolveCall{tc.call}, resolver.recordedCalls())
			require.NotContains(t, rendered, "broken-link")
		})
	}
}

func TestMarkdown_UnresolvableLinksRenderAsBrokenText(t *testing.T) {
	cases := []struct {
		name          string
		markdown      string
		title         string
		inner         string
		resolverCalls []resolveCall
		mustResolve   bool
	}{
		{
			name:          "unresolved relative path",
			markdown:      "[gone](missing.md)",
			title:         "Broken link: missing.md",
			inner:         "gone",
			resolverCalls: []resolveCall{{fromPath: fromPath, target: "missing.md"}},
			mustResolve:   true,
		},
		{
			name:          "unresolved scheme uri",
			markdown:      "[gone](acdc://docs/missing)",
			title:         "Broken link: acdc://docs/missing",
			inner:         "gone",
			resolverCalls: []resolveCall{{target: "acdc://docs/missing"}},
			mustResolve:   true,
		},
		{name: "absolute path", markdown: "[abs](/etc/passwd)", title: "Broken link: /etc/passwd", inner: "abs"},
		{name: "protocol-relative", markdown: "[pr](//evil.example/x)", title: "Broken link: //evil.example/x", inner: "pr"},
		{name: "relative path with query", markdown: "[q](page.md?x=1)", title: "Broken link: page.md?x=1", inner: "q"},
		{name: "invalid percent escape", markdown: "[bad](bad%zz.md)", title: "Broken link: bad%zz.md", inner: "bad"},
		{name: "empty destination", markdown: "[empty]()", title: "Broken link: ", inner: "empty"},
		{
			name:          "ftp",
			markdown:      "[f](ftp://host/file)",
			title:         "Broken link: ftp://host/file",
			inner:         "f",
			resolverCalls: []resolveCall{{target: "ftp://host/file"}},
		},
		{
			name:          "javascript",
			markdown:      "[x](javascript:alert(1))",
			title:         "Broken link: javascript:alert(1)",
			inner:         "x",
			resolverCalls: []resolveCall{{target: "javascript:alert(1)"}},
		},
		{
			name:          "javascript mixed case",
			markdown:      "[x](JaVaScRiPt:alert(1))",
			title:         "Broken link: JaVaScRiPt:alert(1)",
			inner:         "x",
			resolverCalls: []resolveCall{{target: "JaVaScRiPt:alert(1)"}},
		},
		{
			name:          "data uri",
			markdown:      "[d](data:text/html,hi)",
			title:         "Broken link: data:text/html,hi",
			inner:         "d",
			resolverCalls: []resolveCall{{target: "data:text/html,hi"}},
		},
		{
			name:          "quote in target is escaped",
			markdown:      `[q](<a"b.md>)`,
			title:         `Broken link: a"b.md`,
			inner:         "q",
			resolverCalls: []resolveCall{{fromPath: fromPath, target: `a"b.md`}},
			mustResolve:   true,
		},
		{name: "children markup is kept", markdown: "[**bold** gone](/abs)", title: "Broken link: /abs", inner: "<strong>bold</strong> gone"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := newFakeResolver(nil)
			rendered := renderMarkdown(t, tc.markdown, resolver, false)

			require.Empty(t, anchors(rendered), "broken link must not be navigable: %s", rendered)
			match := brokenPattern.FindStringSubmatch(rendered)
			require.NotNil(t, match, "expected broken-link span in %s", rendered)
			require.Equal(t, tc.title, html.UnescapeString(match[1]))
			require.Equal(t, tc.inner, match[2])
			require.NotContains(t, rendered, "href=")
			calls := resolver.recordedCalls()
			if tc.mustResolve {
				require.Equal(t, tc.resolverCalls, calls)
			}
			for _, call := range calls {
				require.Contains(t, tc.resolverCalls, call, "resolver may only see the full target URI, never a relative path")
			}
		})
	}
}

func TestMarkdown_NonRelativeTargetsNeverReachResolverAsPaths(t *testing.T) {
	resolver := newFakeResolver(map[string]string{
		"/etc/passwd":      "acdc://leak",
		"//evil.example/x": "acdc://leak",
		"page.md?x=1":      "acdc://leak",
		"page.md":          "acdc://leak",
	})
	rendered := renderMarkdown(t, "[a](/etc/passwd) [b](//evil.example/x) [c](page.md?x=1)", resolver, false)

	require.NotContains(t, rendered, "leak")
	require.Equal(t, 3, len(brokenPattern.FindAllString(rendered, -1)))
}

func TestMarkdown_RawHTMLIsNotPassedThrough(t *testing.T) {
	source := strings.Join([]string{
		"<script>alert('block')</script>",
		"",
		"Inline <img src=x onerror=alert(1)> and <iframe src=\"https://evil.example\"></iframe> here.",
		"",
		"<div onclick=\"steal()\">block div</div>",
		"",
		"[click](javascript:alert(2)) <javascript:alert(3)>",
		"",
		"<a href=\"https://evil.example\">raw anchor</a>",
	}, "\n")
	rendered := renderMarkdown(t, source, newFakeResolver(nil), true)

	lower := strings.ToLower(rendered)
	for _, forbidden := range []string{"<script", "<img", "<iframe", "<div", "onerror", "onclick", "href=\"javascript", "evil.example\""} {
		require.NotContains(t, lower, forbidden, "rendered: %s", rendered)
	}
	require.Contains(t, rendered, "Inline")
	require.Empty(t, anchors(rendered), "no navigable link may survive: %s", rendered)
}

func TestMarkdown_ImagesRenderAsPlaceholders(t *testing.T) {
	cases := []struct {
		name     string
		markdown string
		title    string
		text     string
	}{
		{name: "relative image", markdown: "![Architecture diagram](img/arch.png)", title: "Image not shown: img/arch.png", text: "Architecture diagram"},
		{name: "remote image", markdown: "![tracker](https://evil.example/t.png)", title: "Image not shown: https://evil.example/t.png", text: "tracker"},
		{name: "empty alt", markdown: "![](img/x.png)", title: "Image not shown: img/x.png", text: "image"},
		{name: "alt escaped", markdown: "![a & b <c>](x.png)", title: "Image not shown: x.png", text: "a &amp; b &lt;c&gt;"},
		{name: "quote in src escaped", markdown: `![q](<a"b.png>)`, title: `Image not shown: a"b.png`, text: "q"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rendered := renderMarkdown(t, tc.markdown, newFakeResolver(nil), false)

			require.NotContains(t, strings.ToLower(rendered), "<img")
			require.NotContains(t, rendered, "src=")
			match := imagePattern.FindStringSubmatch(rendered)
			require.NotNil(t, match, "expected image placeholder in %s", rendered)
			require.Equal(t, tc.title, html.UnescapeString(match[1]))
			require.Equal(t, tc.text, match[2])
		})
	}
}

func TestMarkdown_HeadingIDsOnlyForWholeDocuments(t *testing.T) {
	source := "Intro.\n\n# Setup\n\n- ## Setup\n\n## Setup\n\n> ### Document\n\n#\n"

	withIDs := renderMarkdown(t, source, newFakeResolver(nil), true)
	ids := headingIDPattern.FindAllStringSubmatch(withIDs, -1)
	var got []string
	for _, match := range ids {
		got = append(got, match[1])
	}
	require.Equal(t, []string{"setup", "setup-2", "setup-1", "document-1"}, got)

	withoutIDs := renderMarkdown(t, source, newFakeResolver(nil), false)
	for _, tag := range headingTagPattern.FindAllString(withoutIDs, -1) {
		require.NotContains(t, tag, "id=", "search results must not carry heading ids: %s", withoutIDs)
	}
	require.Contains(t, withoutIDs, "<h1>Setup</h1>")
}

func TestMarkdown_RendersGitHubFlavouredMarkdown(t *testing.T) {
	source := strings.Join([]string{
		"| Left | Right | Centre |",
		"|:-----|------:|:------:|",
		"| a    | b     | c      |",
		"",
		"~~gone~~",
		"",
		"- [x] done",
		"- [ ] todo",
	}, "\n")
	rendered := renderMarkdown(t, source, newFakeResolver(nil), false)

	require.Contains(t, rendered, "<table>")
	require.Contains(t, rendered, `align="left"`)
	require.Contains(t, rendered, `align="right"`)
	require.Contains(t, rendered, `align="center"`)
	require.NotContains(t, rendered, "style=")
	require.Contains(t, rendered, "<del>gone</del>")
	require.Equal(t, 2, strings.Count(rendered, `type="checkbox"`))
}

func TestMarkdown_IsConcurrencySafe(t *testing.T) {
	source := "# Title\n\n[a](sibling.md) [b](missing.md) [c](https://example.com) ![i](x.png)\n\n## Title\n"
	resolver := newFakeResolver(map[string]string{"sibling.md": "acdc://guides/sibling"})
	want := renderMarkdown(t, source, resolver, true)

	const workers = 16
	results := make([]string, workers)
	var wg sync.WaitGroup
	for index := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[index] = render.Markdown(source, render.Options{FromPath: fromPath, HeadingIDs: true, Links: resolver})
		}()
	}
	wg.Wait()

	for _, got := range results {
		require.Equal(t, want, got)
	}
}
