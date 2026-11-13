package render_test

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

var goldmarkAltPattern = regexp.MustCompile(`(?s)<img [^>]*alt="([^"]*)"`)

func renderedAlt(t *testing.T, source string) string {
	t.Helper()
	rendered := renderMarkdown(t, source, newFakeResolver(nil), false)
	match := imagePattern.FindStringSubmatch(rendered)
	require.NotNil(t, match, rendered)
	return match[2]
}

func goldmarkAlt(t *testing.T, source string) string {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, goldmark.New(goldmark.WithExtensions(extension.GFM)).Convert([]byte(source), &out))
	match := goldmarkAltPattern.FindStringSubmatch(out.String())
	require.NotNil(t, match, out.String())
	return match[1]
}

func TestMarkdown_ImageAltKeepsAutolinkText(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		wantAlt string
	}{
		{name: "url autolink", source: "![see https://example.com now](x.png)", wantAlt: "see https://example.com now"},
		{name: "www autolink", source: "![see www.example.com now](x.png)", wantAlt: "see www.example.com now"},
		{name: "angle autolink", source: "![see <https://example.com> now](x.png)", wantAlt: "see https://example.com now"},
		{name: "email autolink", source: "![mail ops@example.com now](x.png)", wantAlt: "mail ops@example.com now"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.wantAlt, renderedAlt(t, tt.source))
		})
	}
}

func TestMarkdown_ImageAltSoftLineBreakMatchesGoldmark(t *testing.T) {
	source := "![first line\nsecond line](x.png)"

	want := goldmarkAlt(t, source)

	require.Equal(t, "first line\nsecond line", want)
	require.Equal(t, want, renderedAlt(t, source))
}
