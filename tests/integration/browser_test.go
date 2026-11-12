package integration

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	handbookURI   = "acdc://alpha/handbook"
	notesURI      = "acdc://beta/notes"
	collisionsURI = "acdc://delta/collisions"
)

func TestBrowser_TypingQueryShowsResults(t *testing.T) {
	s := startBrowserSession(t)
	s.openSearchPage()

	s.typeQuery("orbit")

	s.waitForResultCount(3)
}

func TestBrowser_LabelChipNarrowsAndRemovingItWidens(t *testing.T) {
	s := startBrowserSession(t)
	s.openSearchPage()
	s.typeQuery("orbit")
	s.waitForResultCount(3)

	s.typeQuery(" alp")
	s.acceptSuggestionByClick("alpha")

	s.waitForResultCount(1)
	assert.Equal(t, []string{"alpha"}, s.chipLabels())
	assert.True(t, s.resultsAllCarryLabel("alpha"))

	s.run(chromedp.Click(chromedp.CSS(chipSelector("alpha") + ` button.chip-remove`)))

	s.waitForResultCount(3)
	assert.Empty(t, s.chipLabels())
}

func TestBrowser_SelectingResultExpandsPreview(t *testing.T) {
	s := startBrowserSession(t)
	s.openSearchPage()
	s.typeQuery("zephyr")
	s.waitForResultCount(1)
	article := resultSelector(s.resultChunkIDs()[0])

	s.run(chromedp.Click(chromedp.CSS(article + ` button.result-header`)))

	s.run(
		chromedp.WaitReady(chromedp.CSS(article+` button.result-header[aria-expanded="true"]`)),
		chromedp.WaitVisible(chromedp.CSS(article+` .result-preview a.open-document`)),
	)
}

func TestBrowser_OpenDocumentLandsOnHeadingAndBackRestoresResults(t *testing.T) {
	s := startBrowserSession(t)
	s.openSearchPage()
	s.typeQuery("zephyr")
	s.waitForResultCount(1)
	before := s.resultChunkIDs()
	article := resultSelector(before[0])
	s.run(chromedp.Click(chromedp.CSS(article + ` button.result-header`)))
	s.run(chromedp.WaitVisible(chromedp.CSS(article + ` .result-preview a.open-document`)))

	s.run(chromedp.Click(chromedp.CSS(article + ` .result-preview a.open-document`)))

	s.waitForDocumentState("ready")
	loc := s.location()
	assert.Equal(t, "/ui/doc", loc.Path)
	assert.Equal(t, handbookURI, loc.Query().Get("uri"))
	assert.Equal(t, "zephyr-calibration", loc.Fragment)
	s.waitForHeadingInViewport("zephyr-calibration")

	s.goBack()

	s.waitForResultCount(len(before))
	assert.Equal(t, before, s.resultChunkIDs())
	assert.Equal(t, "zephyr", s.inputValue())
}

func TestBrowser_LinkBetweenDocumentsNavigatesToSecond(t *testing.T) {
	s := startBrowserSession(t)
	s.run(chromedp.Navigate(s.documentPageURL(handbookURI, "")))
	s.waitForDocumentState("ready")

	s.clickDocumentLink("Read the notes")

	s.waitForDocumentTitle("Notes")
	s.waitForDocumentState("ready")
	assert.Equal(t, notesURI, s.location().Query().Get("uri"))
}

func TestBrowser_SectionLinkOpensSecondDocumentAtHeading(t *testing.T) {
	s := startBrowserSession(t)
	s.run(chromedp.Navigate(s.documentPageURL(handbookURI, "")))
	s.waitForDocumentState("ready")

	s.clickDocumentLink("Jump to deep section")

	s.waitForDocumentTitle("Notes")
	s.waitForDocumentState("ready")
	s.waitForHeadingInViewport("deep-section")
}

func TestBrowser_BrokenLinkIsNotNavigable(t *testing.T) {
	s := startBrowserSession(t)
	s.run(chromedp.Navigate(s.documentPageURL(handbookURI, "")))
	s.waitForDocumentState("ready")

	anchors := evalJS[int](s, `Array.from(document.querySelectorAll('#docContent a')).filter(a => a.textContent.includes('Ghost page')).length`)
	shown := evalJS[int](s, `Array.from(document.querySelectorAll('#docContent .broken-link')).filter(e => e.textContent.includes('Ghost page')).length`)

	assert.Zero(t, anchors, "a broken link must not render as an anchor")
	assert.Equal(t, 1, shown, "a broken link must stay visible as text")
}

func TestBrowser_SearchStateSurvivesReload(t *testing.T) {
	s := startBrowserSession(t)
	s.openSearchPage()
	s.typeQuery("orbit alp")
	s.acceptSuggestionByClick("alpha")
	s.waitForResultCount(1)
	before := s.resultChunkIDs()

	s.run(chromedp.Reload())

	s.waitForResultCount(1)
	assert.Equal(t, before, s.resultChunkIDs())
	assert.Equal(t, []string{"alpha"}, s.chipLabels())
	assert.Equal(t, "orbit", strings.TrimSpace(s.inputValue()))
	query := s.location().Query()
	assert.Equal(t, []string{"alpha"}, query["label"])
}

func TestBrowser_ThemeToggleChangesThemeAndPersistsAcrossPages(t *testing.T) {
	s := startBrowserSession(t)
	s.openSearchPage()
	initial := s.theme()
	require.Contains(t, []string{"light", "dark"}, initial)

	toggled := initial
	for range 3 {
		s.run(chromedp.Click(chromedp.CSS(`#themeToggle`)))
		if toggled = s.theme(); toggled != initial {
			break
		}
	}
	require.NotEqual(t, initial, toggled, "theme toggle never changed html[data-theme]")

	s.run(chromedp.Navigate(s.documentPageURL(handbookURI, "")))
	s.waitForDocumentState("ready")

	assert.Equal(t, toggled, s.theme())
}

func TestBrowser_APIFailureShowsErrorState(t *testing.T) {
	s := startBrowserSession(t)
	s.openSearchPage()

	s.failRequests("*/api/v1/*")
	s.typeQuery("orbit")

	s.waitForSearchState("error")
}

func TestBrowser_KeyboardOnlyLabelFlow(t *testing.T) {
	s := startBrowserSession(t)
	s.openSearchPage()
	s.run(chromedp.Focus(chromedp.CSS(`#searchQuery`)))

	s.run(chromedp.KeyEvent("bet"))
	s.run(chromedp.WaitVisible(chromedp.CSS(`#searchSuggest li[role="option"][data-label="beta"]`)))
	s.run(chromedp.KeyEvent(kb.Escape))
	s.run(chromedp.WaitNotVisible(chromedp.CSS(`#searchSuggest`)))

	s.run(chromedp.KeyEvent(kb.Backspace + kb.Backspace + kb.Backspace + "alp"))
	s.run(chromedp.WaitVisible(chromedp.CSS(`#searchSuggest li[role="option"][data-label="alpha"]`)))
	s.run(chromedp.KeyEvent(kb.ArrowDown), chromedp.KeyEvent(kb.Enter))

	s.run(chromedp.WaitReady(chromedp.CSS(chipSelector("alpha"))))
	s.waitForSearchState("results")
	assert.True(t, s.resultsAllCarryLabel("alpha"))
	assert.Empty(t, s.inputValue())

	s.run(chromedp.KeyEvent(kb.Backspace))

	s.waitUntil(`document.querySelectorAll('#searchChips .chip[data-label]').length === 0`)
}

func TestBrowser_UnknownDocumentShowsNotFound(t *testing.T) {
	s := startBrowserSession(t)

	s.run(chromedp.Navigate(s.documentPageURL("acdc://does/not/exist", "")))

	s.waitForDocumentState("not-found")
}

func TestBrowser_DocumentPageWithoutURIShowsMissingURI(t *testing.T) {
	s := startBrowserSession(t)

	s.run(chromedp.Navigate(s.baseURL + "/ui/doc"))

	s.waitForDocumentState("missing-uri")
}

func TestBrowser_HeadingsNamedLikePageIDsKeepEveryIDUnique(t *testing.T) {
	s := startBrowserSession(t)

	s.run(chromedp.Navigate(s.documentPageURL(collisionsURI, "")))
	s.waitForDocumentState("ready")

	assert.Empty(t, evalJS[[]string](s, duplicateIDsJS))
	assert.Equal(t, "Collisions", evalJS[string](s, `document.getElementById('docTitle').textContent.trim()`))
	for _, id := range []string{"doc_content", "search_results", "back_to_search", "theme_toggle", "doccontent", "searchresults"} {
		assert.Truef(t, evalJS[bool](s, fmt.Sprintf(`document.getElementById(%q)?.matches('#docContent :is(h1, h2, h3, h4, h5, h6)') === true`, id)),
			"heading id %q must resolve to a heading inside #docContent", id)
	}
}

func TestBrowser_DocumentTabOrderSkipsHiddenElements(t *testing.T) {
	s := startBrowserSession(t)
	s.run(chromedp.Navigate(s.documentPageURL(handbookURI, "")))
	s.waitForDocumentState("ready")

	reachedHeadingAnchor := false
	for range maxTabStops {
		s.run(chromedp.KeyEvent(kb.Tab))
		stop := evalJS[focusStop](s, focusStopJS)
		if stop.OnBody {
			break
		}
		assert.Emptyf(t, stop.Problem, "Tab focused %s", stop.Description)
		reachedHeadingAnchor = reachedHeadingAnchor || stop.HeadingAnchor
	}

	assert.True(t, reachedHeadingAnchor, "Tab never reached a heading anchor, so the walk did not cover the content")
}

func TestBrowser_WordThatIsALabelSearchesOnDebounceWhileSuggested(t *testing.T) {
	s := startBrowserSession(t)
	s.openSearchPage()
	searches := s.recordRequests("/api/v1/search")

	s.typeQuery("archive")
	s.run(chromedp.WaitVisible(chromedp.CSS(suggestionSelector("archive"))))

	s.waitUntilWithin(debouncedSearchDeadline, `document.querySelector('#searchStatus[data-state="results"]') !== null`)
	assert.Equal(t, "archive", s.location().Query().Get("q"))
	assert.Empty(t, s.chipLabels())

	before := len(searches.urls())
	s.acceptSuggestionByClick("archive")

	committed := searches.waitForCount(t, before+1)
	last := committed[len(committed)-1]
	assert.Empty(t, strings.TrimSpace(last.Query().Get("q")), "accepting must remove the token from the text query")
	assert.Equal(t, []string{"archive"}, last.Query()["label"])
	assert.Empty(t, strings.TrimSpace(s.inputValue()))
	s.waitForSearchState("results")
	assert.True(t, s.resultsAllCarryLabel("archive"))
}

func TestBrowser_EnterKeepsSuggestionsClosedWhenLateLabelsArrive(t *testing.T) {
	s := startBrowserSession(t)
	s.openSearchPage()
	labels := s.recordRequests("/api/v1/labels")
	settled := s.delayRequests("*/api/v1/labels*", lateLabelsDelay)

	s.typeQuery("orbit arch")
	labels.waitForCount(t, 1)
	s.run(chromedp.KeyEvent(kb.Enter))
	s.waitForSearchState("results")

	settled.waitForCount(t, len(labels.urls()))
	time.Sleep(searchSettleDelay)

	assert.False(t, evalJS[bool](s, `document.getElementById('searchSuggest').checkVisibility()`),
		"a label response that arrives after Enter must not reopen suggestions")
	assert.Equal(t, "orbit arch", s.location().Query().Get("q"))
}

func TestBrowser_IdleResultLimitSurvivesReloadAndDirectLoad(t *testing.T) {
	s := startBrowserSession(t)
	s.openSearchPage()

	s.run(chromedp.Evaluate[chromedp.Void](`(() => {
		const limit = document.getElementById('searchLimit');
		limit.value = '50';
		limit.dispatchEvent(new Event('change', {bubbles: true}));
	})()`))

	s.waitUntil(`new URLSearchParams(location.search).get('limit') === '50'`)

	s.run(chromedp.Reload())
	s.waitForSearchState("idle")
	assert.Equal(t, "50", s.limitValue())
	assert.Equal(t, "50", s.location().Query().Get("limit"))

	s.run(chromedp.Navigate(s.searchPageURL(url.Values{"limit": {"50"}})))
	s.waitForSearchState("idle")
	assert.Equal(t, "50", s.limitValue())
	assert.Equal(t, "50", s.location().Query().Get("limit"))
}

func TestBrowser_BackFromDocumentReRunsSearchAndKeepsExpandedResult(t *testing.T) {
	s := startBrowserSession(t)
	s.openSearchPage()
	s.typeQuery("zephyr")
	s.waitForResultCount(1)
	article := resultSelector(s.resultChunkIDs()[0])
	s.run(chromedp.Click(chromedp.CSS(article + ` button.result-header`)))
	s.run(chromedp.WaitVisible(chromedp.CSS(article + ` .result-preview a.open-document`)))
	searches := s.recordRequests("/api/v1/search")
	s.run(chromedp.Click(chromedp.CSS(article + ` .result-preview a.open-document`)))
	s.waitForDocumentState("ready")
	before := len(searches.urls())

	s.goBack()

	s.waitForResultCount(1)
	searches.waitForCount(t, before+1)
	s.waitUntil(fmt.Sprintf(`document.querySelector(%q) !== null`, article+` button.result-header[aria-expanded="true"]`))
	assert.Equal(t, "zephyr", s.inputValue())
}

func TestBrowser_SearchInputShowsFocusRingWhenTabbedTo(t *testing.T) {
	s := startBrowserSession(t)
	s.openSearchPage()
	s.run(chromedp.Evaluate[chromedp.Void](`document.activeElement?.blur()`))

	reached := false
	for range maxTabStops {
		s.run(chromedp.KeyEvent(kb.Tab))
		if reached = evalJS[bool](s, `document.activeElement?.id === 'searchQuery'`); reached {
			break
		}
	}
	require.True(t, reached, "Tab never reached #searchQuery")

	assert.GreaterOrEqual(t, evalJS[float64](s, focusRingContrastJS("searchQuery")), minFocusRingContrast,
		"#searchQuery needs an outline or box-shadow ring on it or its search box with at least 3:1 contrast against the surrounding surface")
}
