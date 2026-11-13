package integration

import (
	"testing"

	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

func TestWebUIAPI_AutoLinkOnlyHeadingHasID(t *testing.T) {
	baseURL, _ := testkit.StartSSE(t,
		&testkit.ContentDirOptions{Resources: map[string]string{
			"autolink.md": "---\nname: Autolink\ndescription: Autolink\n---\n" +
				"# Overview\n\nA.\n\n# <https://example.com>\n\nB.\n\n# Overview\n\nC.\n",
		}},
		&testkit.FlagOptions{UIEnabled: true})

	doc := webUIDocumentByURI(t, baseURL, "acdc://autolink")

	require.Equal(t, []string{"overview", "httpsexamplecom", "overview-1"}, headingIDs(doc.ContentHTML))
	require.Regexp(t, `<h1 id="httpsexamplecom"><a href="https://example.com"[^>]*>https://example.com</a></h1>`, doc.ContentHTML)
}
