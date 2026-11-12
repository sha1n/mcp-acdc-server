package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrowser_ReplacedTokenCannotAcceptStaleSuggestion(t *testing.T) {
	cases := []struct {
		name      string
		selectKey string
		acceptKey string
	}{
		{name: "Tab", acceptKey: kb.Tab},
		{name: "Enter on highlighted option", selectKey: kb.ArrowDown, acceptKey: kb.Enter},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := startBrowserSession(t)
			s.openSearchPage()
			labels := s.recordRequests("/api/v1/labels")
			s.delayRequests("*/api/v1/labels*", lateLabelsDelay)
			s.run(chromedp.Focus(chromedp.CSS(`#searchQuery`)))

			s.run(chromedp.KeyEvent("alp"))
			s.run(chromedp.WaitVisible(chromedp.CSS(suggestionSelector("alpha"))))
			if tc.selectKey != "" {
				s.run(chromedp.KeyEvent(tc.selectKey))
			}

			s.run(chromedp.Evaluate[chromedp.Void](`document.getElementById('searchQuery').select()`))
			s.run(chromedp.KeyEvent("gam"))
			waitForLabelsRequestFor(t, labels, "gam")

			s.run(chromedp.KeyEvent(tc.acceptKey))

			assert.Empty(t, s.chipLabels(), "a suggestion listed for the replaced token must not be accepted")
			assert.Equal(t, "gam", s.inputValue())
		})
	}
}

func TestBrowser_PreviewTitleLinksToDocument(t *testing.T) {
	s := startBrowserSession(t)
	s.openSearchPage()
	s.typeQuery("zephyr")
	s.waitForResultCount(1)
	article := resultSelector(s.resultChunkIDs()[0])
	s.run(chromedp.Click(chromedp.CSS(article + ` button.result-header`)))
	s.run(chromedp.WaitVisible(chromedp.CSS(article + ` .result-preview a.open-document`)))

	title := evalJS[string](s, fmt.Sprintf(`document.querySelector(%q).textContent.trim()`, article+` .result-title`))
	documentURL := evalJS[string](s, fmt.Sprintf(`document.querySelector(%q).getAttribute('href')`, article+` .result-preview a.open-document`))
	require.NotEmpty(t, title)
	require.NotEmpty(t, documentURL)

	titleLinks := evalJS[[]string](s, fmt.Sprintf(`Array.from(document.querySelectorAll(%q))
		.filter(a => !a.classList.contains('open-document') && a.textContent.trim() === %q)
		.filter(a => !a.hasAttribute('role') && !a.hasAttribute('target') && a.closest('button') === null)
		.map(a => a.getAttribute('href'))`, article+` .result-preview a[href]`, title))
	require.Equal(t, []string{documentURL}, titleLinks, "the expanded preview must link its title %q to document_url", title)
	assert.True(t, evalJS[bool](s, fmt.Sprintf(`document.querySelector(%q) !== null`,
		article+` button.result-header[aria-expanded="true"]`)), "the result header must stay a toggle button")

	s.run(chromedp.Click(chromedp.Search(fmt.Sprintf(
		`//article[@data-chunk-id=%q]//div[contains(concat(' ', normalize-space(@class), ' '), ' result-preview ')]//a[not(contains(@class, 'open-document')) and normalize-space()=%q]`,
		s.resultChunkIDs()[0], title))))

	s.waitForDocumentState("ready")
	loc := s.location()
	assert.Equal(t, "/ui/doc", loc.Path)
	assert.Equal(t, handbookURI, loc.Query().Get("uri"))
	assert.Equal(t, "zephyr-calibration", loc.Fragment)
}

func waitForLabelsRequestFor(t *testing.T, labels *requestRecorder, token string) {
	t.Helper()
	require.Eventuallyf(t, func() bool {
		for _, u := range labels.urls() {
			if u.Query().Get("q") == token {
				return true
			}
		}
		return false
	}, requestCountDeadline, 10*time.Millisecond, "expected a labels request for %q", token)
}
