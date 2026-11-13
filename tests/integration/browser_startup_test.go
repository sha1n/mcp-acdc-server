package integration

import (
	"net/url"
	"testing"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const configRequestPattern = "*/api/v1/config*"

// openSearchPageBeforeStartup loads the search page with its config request held,
// so the page stays in its startup window until the returned function is called.
func (s *browserSession) openSearchPageBeforeStartup(query url.Values) (finishStartup func()) {
	s.t.Helper()
	release := s.holdRequests(configRequestPattern)
	s.run(
		chromedp.Navigate(s.searchPageURL(query)),
		chromedp.WaitVisible(chromedp.CSS(`#searchQuery`)),
	)
	return func() {
		s.t.Helper()
		require.False(s.t, evalJS[bool](s, searchPageStartedJS), "the page must still be starting while its config request is held")
		release()
		s.waitUntil(searchPageStartedJS)
	}
}

func TestBrowser_QueryTypedBeforeStartupSurvivesAndIsSearched(t *testing.T) {
	s := startBrowserSession(t)
	finishStartup := s.openSearchPageBeforeStartup(nil)

	s.typeQuery("orbit")
	finishStartup()

	require.Equal(t, "orbit", s.inputValue(), "text typed before startup completed must not be discarded")
	s.waitForResultCount(3)
	assert.Equal(t, "orbit", s.inputValue())
	assert.Equal(t, "orbit", s.location().Query().Get("q"))
}

func TestBrowser_QueryTypedBeforeStartupReplacesDeepLinkedQuery(t *testing.T) {
	s := startBrowserSession(t)
	finishStartup := s.openSearchPageBeforeStartup(url.Values{"q": {"orbit"}})

	s.run(chromedp.Evaluate[chromedp.Void](`document.getElementById('searchQuery').select()`))
	s.typeQuery("zephyr")
	finishStartup()

	require.Equal(t, "zephyr", s.inputValue(), "text typed before startup completed must win over the URL")
	s.waitForResultCount(1)
	assert.Equal(t, "zephyr", s.location().Query().Get("q"))
}

func TestBrowser_UntouchedControlsKeepDeepLinkedStateWhenSearchingBeforeStartup(t *testing.T) {
	s := startBrowserSession(t)
	finishStartup := s.openSearchPageBeforeStartup(url.Values{"label": {"alpha"}, "limit": {"50"}})

	s.typeQuery("orbit")
	s.run(chromedp.KeyEvent(kb.Enter))
	finishStartup()

	s.waitForSearchState("results")
	require.Equal(t, []string{"alpha"}, s.chipLabels(), "a label filter the user did not touch must still come from the URL")
	require.Equal(t, "50", s.limitValue(), "a result limit the user did not touch must still come from the URL")
	require.Equal(t, "orbit", s.inputValue())
	s.waitForResultCount(1)
	query := s.location().Query()
	assert.Equal(t, "orbit", query.Get("q"))
	assert.Equal(t, []string{"alpha"}, query["label"])
	assert.Equal(t, "50", query.Get("limit"))
}

func TestBrowser_DeepLinkStartsFromURLWhenNothingIsTouchedBeforeStartup(t *testing.T) {
	s := startBrowserSession(t)
	finishStartup := s.openSearchPageBeforeStartup(url.Values{"q": {"orbit"}, "label": {"alpha"}, "limit": {"50"}})

	finishStartup()

	s.waitForResultCount(1)
	assert.Equal(t, "orbit", s.inputValue())
	assert.Equal(t, []string{"alpha"}, s.chipLabels())
	assert.Equal(t, "50", s.limitValue())
	query := s.location().Query()
	assert.Equal(t, "orbit", query.Get("q"))
	assert.Equal(t, []string{"alpha"}, query["label"])
	assert.Equal(t, "50", query.Get("limit"))
}
