package integration

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	appScriptRequestPattern = "*/ui/app.js*"

	// A document tagged with tagDocumentJS stays tagged for as long as it lives, so
	// an untagged search page is one the browser built after the tag was set.
	tagDocumentJS             = `window.__acdcTestTaggedDocument = true`
	documentIsTaggedJS        = `window.__acdcTestTaggedDocument === true`
	untaggedSearchDocumentJS  = `location.pathname === '/ui/' && !(` + documentIsTaggedJS + `) && document.getElementById('searchQuery') !== null`
	searchPageFullyLoadedJS   = `location.pathname === '/ui/' && document.readyState === 'complete' && ` + searchPageStartedJS
	navigationTypeJS          = `performance.getEntriesByType('navigation')[0].type`
	backForwardCacheFeature   = "BackForwardCache"
	chromedpDisabledFeatures  = "Translate,BlinkGenPropertyTrees"
	unsearchedQueryAssignment = `document.getElementById('searchQuery').value = 'orbit zephyr'`
)

// withoutBackForwardCache forces history navigation to rebuild the page. The flag
// replaces chromedp's own disable-features value, so that list is repeated here.
func withoutBackForwardCache() chromedp.ExecAllocatorOption {
	return chromedp.Flag("disable-features", chromedpDisabledFeatures+","+backForwardCacheFeature)
}

// openSearchPageBeforeScriptRuns loads the search page with its script request
// held, so the markup is interactive but no handler exists until the returned
// function is called. chromedp.Navigate cannot be used here: it waits for a load
// event that the held script keeps from firing.
func (s *browserSession) openSearchPageBeforeScriptRuns(query url.Values) (runScript func()) {
	s.t.Helper()
	release := s.holdRequests(appScriptRequestPattern)
	_, err := chromedp.Call(s.ctx, page.Navigate, page.NavigateParams{URL: s.searchPageURL(query)})
	require.NoError(s.t, err)
	s.run(chromedp.WaitVisible(chromedp.CSS(`#searchQuery`)))
	return func() {
		s.t.Helper()
		require.False(s.t, evalJS[bool](s, searchPageStartedJS), "the page script must not have run while its request is held")
		release()
		s.waitUntil(searchPageStartedJS)
	}
}

// replaceSearchDocument performs a navigation that ends on a newly built search
// page and returns once that page's markup exists, without waiting for its script.
func (s *browserSession) replaceSearchDocument(navigate func()) {
	s.t.Helper()
	s.run(chromedp.Evaluate[chromedp.Void](tagDocumentJS))
	navigate()
	s.waitUntil(untaggedSearchDocumentJS)
	s.run(chromedp.WaitVisible(chromedp.CSS(`#searchQuery`)))
}

func (s *browserSession) reloadWithoutWaitingForLoad() {
	s.t.Helper()
	_, err := chromedp.Call(s.ctx, page.Reload, page.ReloadParams{})
	require.NoError(s.t, err)
}

func (s *browserSession) leaveForDocumentPage() {
	s.t.Helper()
	s.run(chromedp.Evaluate[chromedp.Void](fmt.Sprintf(`location.assign(%q)`, s.documentPageURL(handbookURI, ""))))
	s.waitForDocumentState("ready")
}

func TestBrowser_QueryTypedBeforeScriptRunsSurvivesAndIsSearched(t *testing.T) {
	s := startBrowserSession(t)
	runScript := s.openSearchPageBeforeScriptRuns(nil)

	s.typeQuery("orbit")
	runScript()

	require.Equal(t, "orbit", s.inputValue(), "text typed before the page script ran must not be discarded")
	s.waitForResultCount(3)
	assert.Equal(t, "orbit", s.inputValue())
	assert.Equal(t, "orbit", s.location().Query().Get("q"))
}

func TestBrowser_QueryTypedBeforeScriptRunsReplacesDeepLinkedQueryAndKeepsItsFilters(t *testing.T) {
	s := startBrowserSession(t)
	runScript := s.openSearchPageBeforeScriptRuns(url.Values{"q": {"orbit"}, "label": {"alpha"}, "limit": {"50"}})

	s.typeQuery("zephyr")
	runScript()

	require.Equal(t, "zephyr", s.inputValue(), "text typed before the page script ran must win over the URL")
	s.waitForResultCount(1)
	assert.Equal(t, "zephyr", s.inputValue())
	assert.Equal(t, []string{"alpha"}, s.chipLabels())
	assert.Equal(t, "50", s.limitValue())
	query := s.location().Query()
	assert.Equal(t, "zephyr", query.Get("q"))
	assert.Equal(t, []string{"alpha"}, query["label"])
	assert.Equal(t, "50", query.Get("limit"))
}

func TestBrowser_QueryTypedBeforeScriptRunsAfterReloadSurvivesAndIsSearched(t *testing.T) {
	s := startBrowserSession(t)
	runScript := s.openSearchPageBeforeScriptRuns(url.Values{"q": {"orbit"}})
	s.replaceSearchDocument(s.reloadWithoutWaitingForLoad)
	require.Equal(t, "reload", evalJS[string](s, navigationTypeJS))

	s.typeQuery("zephyr")
	runScript()

	require.Equal(t, "zephyr", s.inputValue(), "text typed before the page script ran must not be discarded on a reload")
	s.waitForResultCount(1)
	assert.Equal(t, "zephyr", s.location().Query().Get("q"))
}

func TestBrowser_DeepLinkStartsFromURLWhenNothingIsTypedBeforeScriptRuns(t *testing.T) {
	s := startBrowserSession(t)
	runScript := s.openSearchPageBeforeScriptRuns(url.Values{"q": {"orbit"}, "label": {"alpha"}, "limit": {"50"}})

	runScript()

	s.waitForResultCount(1)
	assert.Equal(t, "orbit", s.inputValue())
	assert.Equal(t, []string{"alpha"}, s.chipLabels())
	assert.Equal(t, "50", s.limitValue())
	query := s.location().Query()
	assert.Equal(t, "orbit", query.Get("q"))
	assert.Equal(t, []string{"alpha"}, query["label"])
	assert.Equal(t, "50", query.Get("limit"))
}

func TestBrowser_ReloadStartsFromURLNotFromTextTypedIntoThePreviousPage(t *testing.T) {
	s := startBrowserSession(t)
	runScript := s.openSearchPageBeforeScriptRuns(url.Values{"q": {"orbit"}})
	s.typeQuery("zephyr")
	require.Equal(t, "zephyr", s.inputValue())

	s.replaceSearchDocument(s.reloadWithoutWaitingForLoad)
	require.Equal(t, "reload", evalJS[string](s, navigationTypeJS))
	runScript()

	s.waitUntil(searchPageFullyLoadedJS)
	s.waitForResultCount(3)
	assert.Equal(t, "orbit", s.inputValue(), "text left in the previous page's search box must not override the URL")
	assert.Equal(t, "orbit", s.location().Query().Get("q"))
}

func TestBrowser_BackStartsFromURLNotFromTextTypedIntoThePreviousPage(t *testing.T) {
	s := startBrowserSession(t)
	runScript := s.openSearchPageBeforeScriptRuns(url.Values{"q": {"orbit"}})
	s.typeQuery("zephyr")
	require.Equal(t, "zephyr", s.inputValue())

	s.replaceSearchDocument(func() {
		s.leaveForDocumentPage()
		s.goBack()
	})
	require.Equal(t, "back_forward", evalJS[string](s, navigationTypeJS))
	runScript()

	s.waitUntil(searchPageFullyLoadedJS)
	s.waitForResultCount(3)
	assert.Equal(t, "orbit", s.inputValue(), "text left in the previous page's search box must not override the URL")
	assert.Equal(t, "orbit", s.location().Query().Get("q"))
}

func TestBrowser_BackToStartedSearchPageShowsURLQueryNotUnsearchedText(t *testing.T) {
	cases := []struct {
		name           string
		browserOptions []chromedp.ExecAllocatorOption
		wantCached     bool
	}{
		{name: "restored from the back-forward cache", wantCached: true},
		{name: "rebuilt from history", browserOptions: []chromedp.ExecAllocatorOption{withoutBackForwardCache()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := startBrowserSession(t, tc.browserOptions...)
			s.run(chromedp.Navigate(s.searchPageURL(url.Values{"q": {"orbit"}})))
			s.waitForResultCount(3)
			// Assigned instead of typed: typed text is searched as soon as the debounce
			// elapses, and only text that was never searched can disagree with the URL.
			s.run(
				chromedp.Evaluate[chromedp.Void](unsearchedQueryAssignment),
				chromedp.Evaluate[chromedp.Void](tagDocumentJS),
			)
			s.leaveForDocumentPage()

			s.goBack()

			s.waitUntilWithin(requestCountDeadline,
				searchPageFullyLoadedJS+` && document.getElementById('searchQuery').value === 'orbit'`)
			require.Equal(t, tc.wantCached, evalJS[bool](s, documentIsTaggedJS), "the page must come back the way this case names")
			s.waitForResultCount(3)
			assert.Equal(t, "orbit", s.inputValue(), "text that was never searched must not override the URL")
			assert.Equal(t, "orbit", s.location().Query().Get("q"))
		})
	}
}
