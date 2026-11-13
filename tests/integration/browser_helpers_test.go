package integration

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/require"
)

const (
	browserEnvVar        = "ACDC_TEST_CHROME"
	requireBrowserEnvVar = "ACDC_REQUIRE_BROWSER"
	macOSChromePath      = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	browserTestTimeout   = 45 * time.Second
	maxTabStops          = 60
	minFocusRingContrast = 3.0
	// searchSettleDelay comfortably exceeds the search input's debounce, so any
	// automatic search the page would issue has been sent by the time it elapses.
	searchSettleDelay = 750 * time.Millisecond
	// debouncedSearchDeadline bounds "searches after typing stops" well above the
	// debounce but far below browserTestTimeout, so an indefinite hold fails fast.
	debouncedSearchDeadline = 2 * time.Second
	lateLabelsDelay         = 800 * time.Millisecond
	requestCountDeadline    = 5 * time.Second
)

var (
	browserCandidates = []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"}
	errNoBrowser      = errors.New("no Chrome/Chromium found; set ACDC_TEST_CHROME to a browser binary")
)

type browserLocator struct {
	getenv   func(string) string
	lookPath func(string) (string, error)
	isExec   func(string) bool
}

func systemBrowserLocator() browserLocator {
	return browserLocator{getenv: os.Getenv, lookPath: exec.LookPath, isExec: isExecutableFile}
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// locate treats an explicit ACDC_TEST_CHROME as authoritative: a wrong value is a
// misconfiguration that must fail loudly instead of silently falling back or skipping.
func (l browserLocator) locate() (string, error) {
	if explicit := l.getenv(browserEnvVar); explicit != "" {
		if !l.isExec(explicit) {
			return "", fmt.Errorf("%s=%q is not an executable browser binary", browserEnvVar, explicit)
		}
		return explicit, nil
	}
	for _, name := range browserCandidates {
		if found, err := l.lookPath(name); err == nil {
			return found, nil
		}
	}
	if l.isExec(macOSChromePath) {
		return macOSChromePath, nil
	}
	return "", errNoBrowser
}

// requireBrowserOrSkip returns a browser binary. It skips when none is installed,
// unless ACDC_REQUIRE_BROWSER=1 (set in CI), and fails on an invalid ACDC_TEST_CHROME.
func requireBrowserOrSkip(t *testing.T) string {
	t.Helper()
	path, err := systemBrowserLocator().locate()
	switch {
	case err == nil:
		return path
	case errors.Is(err, errNoBrowser) && os.Getenv(requireBrowserEnvVar) != "1":
		t.Skip(err.Error())
	default:
		t.Fatal(err)
	}
	return ""
}

type browserSession struct {
	t       *testing.T
	ctx     context.Context
	baseURL string
}

func startBrowserSession(t *testing.T) *browserSession {
	t.Helper()
	browserPath := requireBrowserOrSkip(t)

	baseURL, _ := testkit.StartSSE(t,
		&testkit.ContentDirOptions{Resources: browserFixture()},
		&testkit.FlagOptions{UIEnabled: true},
	)

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browserPath),
		chromedp.WindowSize(1024, 768),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	timeoutCtx, cancelTimeout := context.WithTimeout(allocCtx, browserTestTimeout)
	ctx, cancelBrowser := chromedp.NewContext(timeoutCtx)
	t.Cleanup(func() {
		cancelBrowser()
		cancelTimeout()
		cancelAlloc()
	})

	return &browserSession{t: t, ctx: ctx, baseURL: baseURL}
}

func (s *browserSession) run(actions ...chromedp.Action[chromedp.Void]) {
	s.t.Helper()
	require.NoError(s.t, chromedp.Do(s.ctx, actions...))
}

func runFor[T any](s *browserSession, action chromedp.Action[T]) T {
	s.t.Helper()
	v, err := chromedp.Run(s.ctx, action)
	require.NoError(s.t, err)
	return v
}

func evalJS[T any](s *browserSession, expression string) T {
	s.t.Helper()
	return runFor(s, chromedp.Evaluate[T](expression))
}

func (s *browserSession) searchPageURL(query url.Values) string {
	if len(query) == 0 {
		return s.baseURL + "/ui/"
	}
	return s.baseURL + "/ui/?" + query.Encode()
}

func (s *browserSession) documentPageURL(uri, fragment string) string {
	u := s.baseURL + "/ui/doc?uri=" + url.QueryEscape(uri)
	if fragment != "" {
		u += "#" + fragment
	}
	return u
}

func (s *browserSession) openSearchPage() {
	s.t.Helper()
	s.run(chromedp.Navigate(s.searchPageURL(nil)))
	s.waitForSearchState("idle")
	s.run(chromedp.WaitVisible(chromedp.CSS(`#searchQuery`)))
}

func (s *browserSession) typeQuery(text string) {
	s.t.Helper()
	s.run(chromedp.SendKeys(chromedp.CSS(`#searchQuery`), text))
}

func (s *browserSession) waitForResultCount(n int) {
	s.t.Helper()
	s.waitUntil(fmt.Sprintf(
		`document.querySelector('#searchStatus[data-state="results"]') !== null && %s.length === %d`,
		resultArticlesJS, n))
}

func (s *browserSession) waitForSearchState(state string) {
	s.t.Helper()
	s.waitUntil(fmt.Sprintf(`%s && document.querySelector('#searchStatus[data-state=%q]') !== null`, searchPageStartedJS, state))
}

func (s *browserSession) waitForDocumentState(state string) {
	s.t.Helper()
	s.waitUntil(fmt.Sprintf(`document.querySelector('#docStatus[data-state=%q]') !== null`, state))
}

// waitForDocumentTitle tolerates a missing #docTitle because the poll may evaluate
// against the next document before its body has been parsed.
func (s *browserSession) waitForDocumentTitle(title string) {
	s.t.Helper()
	s.waitUntil(fmt.Sprintf(`document.getElementById('docTitle')?.textContent.trim() === %q`, title))
}

func (s *browserSession) waitUntil(expression string) {
	s.t.Helper()
	require.NoError(s.t, pollUntil(s.ctx, expression))
}

func (s *browserSession) waitUntilWithin(deadline time.Duration, expression string) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(s.ctx, deadline)
	defer cancel()
	require.NoErrorf(s.t, pollUntil(ctx, expression), "condition not met within %s", deadline)
}

// pollUntil re-evaluates the expression from Go instead of using chromedp.Poll,
// which can hang on a document reached through history navigation. A navigation
// that destroys the evaluating context only re-arms the poll, so callers can wait
// for a condition on the page that a click or history step leads to.
func pollUntil(ctx context.Context, expression string) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		met, err := chromedp.Run(ctx, chromedp.Evaluate[bool](expression))
		switch {
		case err == nil && met:
			return nil
		case err != nil && ctx.Err() == nil && !isNavigationRace(err):
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w", expression, ctx.Err())
		case <-ticker.C:
		}
	}
}

func isNavigationRace(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "Cannot find context with specified id") ||
		strings.Contains(msg, "Execution context was destroyed")
}

// goBack uses history.back() because chromedp.NavigateBack waits for a load event
// that a back/forward-cache restore never fires.
func (s *browserSession) goBack() {
	s.t.Helper()
	s.run(chromedp.Evaluate[chromedp.Void](`history.back()`))
}

func (s *browserSession) acceptSuggestionByClick(label string) {
	s.t.Helper()
	option := suggestionSelector(label)
	s.run(
		chromedp.WaitVisible(chromedp.CSS(option)),
		chromedp.Click(chromedp.CSS(option)),
		chromedp.WaitReady(chromedp.CSS(chipSelector(label))),
	)
}

func (s *browserSession) resultChunkIDs() []string {
	s.t.Helper()
	return evalJS[[]string](s, resultArticlesJS+`.map(a => a.dataset.chunkId)`)
}

func (s *browserSession) resultsAllCarryLabel(label string) bool {
	s.t.Helper()
	return evalJS[bool](s, fmt.Sprintf(`%s.every(a => a.querySelector('.result-labels button.label[data-label=%q]') !== null)`,
		resultArticlesJS, label))
}

func (s *browserSession) chipLabels() []string {
	s.t.Helper()
	return evalJS[[]string](s, `Array.from(document.querySelectorAll('#searchChips .chip[data-label]')).map(c => c.dataset.label)`)
}

func (s *browserSession) limitValue() string {
	s.t.Helper()
	return evalJS[string](s, `document.getElementById('searchLimit').value`)
}

func (s *browserSession) inputValue() string {
	s.t.Helper()
	return evalJS[string](s, `document.getElementById('searchQuery').value`)
}

func (s *browserSession) theme() string {
	s.t.Helper()
	return evalJS[string](s, `document.documentElement.dataset.theme || ""`)
}

func (s *browserSession) location() *url.URL {
	s.t.Helper()
	u, err := url.Parse(runFor(s, chromedp.Location()))
	require.NoError(s.t, err)
	return u
}

// waitForHeadingInViewport also requires a non-zero scroll offset so a heading that
// happens to render above the fold cannot satisfy "opened at the right heading".
func (s *browserSession) waitForHeadingInViewport(id string) {
	s.t.Helper()
	s.waitUntil(fmt.Sprintf(`(() => {
		const el = document.getElementById(%q);
		if (!el || window.scrollY <= 0) return false;
		const r = el.getBoundingClientRect();
		return r.top >= 0 && r.top < window.innerHeight;
	})()`, id))
}

func (s *browserSession) clickDocumentLink(text string) {
	s.t.Helper()
	s.run(chromedp.Click(chromedp.Search(fmt.Sprintf(`//div[@id="docContent"]//a[normalize-space()=%q]`, text))))
}

// requestRecorder collects request URLs on the browser's network goroutine, so
// reads and writes are serialised.
type requestRecorder struct {
	mu   sync.Mutex
	seen []*url.URL
}

func (r *requestRecorder) urls() []*url.URL {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*url.URL(nil), r.seen...)
}

func (r *requestRecorder) record(u *url.URL) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, u)
}

func (r *requestRecorder) waitForCount(t *testing.T, n int) []*url.URL {
	t.Helper()
	var seen []*url.URL
	require.Eventuallyf(t, func() bool {
		seen = r.urls()
		return len(seen) >= n
	}, requestCountDeadline, 25*time.Millisecond, "expected at least %d requests", n)
	return seen
}

// delayRequests pauses matching requests through the Fetch domain and releases
// each one after delay, recording it once settled: released, or already
// cancelled by the page (Chrome then rejects the interception id). The release
// runs on its own goroutine because a Listen callback must not issue protocol
// commands.
func (s *browserSession) delayRequests(urlPattern string, delay time.Duration) *requestRecorder {
	s.t.Helper()
	settled := &requestRecorder{}
	require.NoError(s.t, chromedp.Listen(s.ctx, func(ev any) {
		paused, ok := ev.(*fetch.EventRequestPaused)
		if !ok {
			return
		}
		go func() {
			time.Sleep(delay)
			_, _ = chromedp.Call(s.ctx, fetch.ContinueRequest, fetch.ContinueRequestParams{RequestID: paused.RequestID})
			if u, err := url.Parse(paused.Request.URL); err == nil {
				settled.record(u)
			}
		}()
	}))
	_, err := chromedp.Call(s.ctx, fetch.Enable, fetch.EnableParams{Patterns: []*fetch.RequestPattern{{URLPattern: urlPattern}}})
	require.NoError(s.t, err)
	return settled
}

// holdRequests pauses matching requests through the Fetch domain until the
// returned release function is called, so a test can act while a response is
// outstanding without depending on how long those actions take.
func (s *browserSession) holdRequests(urlPattern string) (release func()) {
	s.t.Helper()
	released := make(chan struct{})
	require.NoError(s.t, chromedp.Listen(s.ctx, func(ev any) {
		paused, ok := ev.(*fetch.EventRequestPaused)
		if !ok {
			return
		}
		go func() {
			select {
			case <-released:
			case <-s.ctx.Done():
				return
			}
			_, _ = chromedp.Call(s.ctx, fetch.ContinueRequest, fetch.ContinueRequestParams{RequestID: paused.RequestID})
		}()
	}))
	_, err := chromedp.Call(s.ctx, fetch.Enable, fetch.EnableParams{Patterns: []*fetch.RequestPattern{{URLPattern: urlPattern}}})
	require.NoError(s.t, err)
	return sync.OnceFunc(func() { close(released) })
}

// failRequests makes matching requests fail as if the server refused the
// connection. Stopping the real server mid-test is not equivalent: Chrome keeps
// speculative connections open without sending a request, and http.Server.Shutdown
// waits up to five seconds before closing such StateNew connections.
func (s *browserSession) failRequests(urlPattern string) {
	s.t.Helper()
	require.NoError(s.t, chromedp.Listen(s.ctx, func(ev any) {
		paused, ok := ev.(*fetch.EventRequestPaused)
		if !ok {
			return
		}
		go func() {
			_, _ = chromedp.Call(s.ctx, fetch.FailRequest, fetch.FailRequestParams{
				RequestID:   paused.RequestID,
				ErrorReason: network.ErrorReasonConnectionRefused,
			})
		}()
	}))
	_, err := chromedp.Call(s.ctx, fetch.Enable, fetch.EnableParams{Patterns: []*fetch.RequestPattern{{URLPattern: urlPattern}}})
	require.NoError(s.t, err)
}

func (s *browserSession) recordRequests(path string) *requestRecorder {
	s.t.Helper()
	recorder := &requestRecorder{}
	require.NoError(s.t, chromedp.Listen(s.ctx, func(ev any) {
		sent, ok := ev.(*network.EventRequestWillBeSent)
		if !ok {
			return
		}
		u, err := url.Parse(sent.Request.URL)
		if err != nil || u.Path != path {
			return
		}
		recorder.record(u)
	}))
	return recorder
}

type focusStop struct {
	OnBody        bool   `json:"onBody"`
	Description   string `json:"description"`
	Problem       string `json:"problem"`
	HeadingAnchor bool   `json:"headingAnchor"`
}

const duplicateIDsJS = `(() => {
	const seen = new Set(), duplicates = new Set();
	for (const el of document.querySelectorAll('[id]')) {
		if (seen.has(el.id)) duplicates.add(el.id);
		seen.add(el.id);
	}
	return [...duplicates];
})()`

// focusStopJS reports why the focused element cannot be seen. Clipping by an
// overflow ancestor is checked explicitly because checkVisibility ignores it.
const focusStopJS = `(() => {
	const el = document.activeElement;
	if (!el || el === document.body || el === document.documentElement) return {onBody: true};
	const description = el.outerHTML.slice(0, 160);
	const result = {onBody: false, description, problem: '', headingAnchor: el.matches('a.heading-anchor')};
	if (el.closest('[aria-hidden="true"]')) { result.problem = 'inside aria-hidden'; return result; }
	if (!el.checkVisibility({opacityProperty: true, visibilityProperty: true})) { result.problem = 'not rendered'; return result; }
	const r = el.getBoundingClientRect();
	if (r.width < 1 || r.height < 1) { result.problem = 'zero size'; return result; }
	for (let a = el.parentElement; a && a !== document.body; a = a.parentElement) {
		const cs = getComputedStyle(a);
		if (cs.overflowX === 'visible' && cs.overflowY === 'visible') continue;
		const c = a.getBoundingClientRect();
		const overlaps = r.right > c.left && r.left < c.right && r.bottom > c.top && r.top < c.bottom;
		if (c.width < 1 || c.height < 1 || !overlaps) { result.problem = 'clipped by ' + a.tagName.toLowerCase(); return result; }
	}
	return result;
})()`

// focusRingContrastJS returns the best WCAG contrast between a focus ring (outline
// or box-shadow) on the element or one of its three nearest ancestors and the
// surface behind it. Colours are resolved through a canvas so any CSS colour
// syntax and alpha compositing are handled the way the browser paints them.
func focusRingContrastJS(id string) string {
	return fmt.Sprintf(`(() => {
	const canvas = document.createElement('canvas');
	canvas.width = canvas.height = 1;
	const ctx = canvas.getContext('2d', {willReadFrequently: true});
	const paint = (...layers) => {
		ctx.clearRect(0, 0, 1, 1);
		for (const colour of layers) { ctx.fillStyle = colour; ctx.fillRect(0, 0, 1, 1); }
		return Array.from(ctx.getImageData(0, 0, 1, 1).data);
	};
	const css = rgba => 'rgb(' + rgba.slice(0, 3).join(',') + ')';
	const luminance = rgba => {
		const [r, g, b] = rgba.slice(0, 3).map(v => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; });
		return 0.2126 * r + 0.7152 * g + 0.0722 * b;
	};
	const contrast = (a, b) => { const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x); return (hi + 0.05) / (lo + 0.05); };
	const surface = el => {
		for (let a = el.parentElement; a; a = a.parentElement) {
			const bg = getComputedStyle(a).backgroundColor;
			if (paint(bg)[3] > 0) return css(paint('#fff', bg));
		}
		return '#fff';
	};
	const rings = el => {
		const cs = getComputedStyle(el), colours = [];
		if (cs.outlineStyle !== 'none' && parseFloat(cs.outlineWidth) >= 1) colours.push(cs.outlineColor);
		if (cs.boxShadow !== 'none') {
			for (const shadow of cs.boxShadow.split(/,(?![^()]*\))/)) {
				const colour = shadow.match(/[a-z-]+\([^()]*\)/i);
				const lengths = shadow.replace(colour ? colour[0] : '', '').match(/-?[\d.]+px/g) || [];
				if (colour && lengths.some(l => parseFloat(l) !== 0)) colours.push(colour[0]);
			}
		}
		return colours;
	};
	let best = 0, el = document.getElementById(%q);
	for (let depth = 0; el && el !== document.body && depth < 4; depth++, el = el.parentElement) {
		const bg = surface(el), bgRGBA = paint(bg);
		for (const colour of rings(el)) best = Math.max(best, contrast(paint(bg, colour), bgRGBA));
	}
	return best;
})()`, id)
}

// searchPageStartedJS guards waits on #searchStatus: the served markup already
// carries data-state="idle", so the state alone cannot tell a started page from
// one still fetching its config. The limit options are rendered in the same task
// that first sets the state from script, once the config request has settled.
const searchPageStartedJS = `document.getElementById('searchLimit')?.options.length > 0`

const resultArticlesJS = `Array.from(document.querySelectorAll('#searchResults article.result[data-chunk-id]'))`

func suggestionSelector(label string) string {
	return fmt.Sprintf(`#searchSuggest li[role="option"][data-label=%q]`, label)
}

func chipSelector(label string) string {
	return fmt.Sprintf(`#searchChips .chip[data-label=%q]`, label)
}

func resultSelector(chunkID string) string {
	return fmt.Sprintf(`#searchResults article.result[data-chunk-id=%q]`, chunkID)
}

func paragraphs(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "Filler paragraph %d keeps the interesting section well below the fold.\n\n", i+1)
	}
	return b.String()
}

// browserFixture spreads the query term "orbit" over exactly one section per document so
// result counts are exact, and keeps every other term unique to a single section.
func browserFixture() map[string]string {
	return map[string]string{
		"alpha/handbook.md": "---\nname: Handbook\ndescription: Alpha handbook\nkeywords:\n  - navigation\n---\n" +
			"# Handbook\n\nThe handbook explains orbit planning.\n\n" +
			"See [Read the notes](../beta/notes.md) and [Jump to deep section](../beta/notes.md#deep-section).\n\n" +
			"This [Ghost page](missing.md) does not exist.\n\n" +
			"## Background\n\n" + paragraphs(80) +
			"## Zephyr Calibration\n\nZephyr calibration aligns the instruments.\n",
		"beta/notes.md": "---\nname: Notes\ndescription: Beta notes\nkeywords:\n  - navigation\n---\n" +
			"# Notes\n\nThe notes record orbit observations.\n\n" +
			"Back to [the handbook](../alpha/handbook.md).\n\n" +
			"## Padding\n\n" + paragraphs(80) +
			"## Deep Section\n\nQuasar measurements live here.\n",
		"delta/collisions.md": "---\nname: Collisions\ndescription: Delta collisions\nkeywords:\n  - clash\n---\n" +
			"# Collisions\n\nHeadings below are named like the page's own element ids.\n\n" +
			"## doc_content\n\nFirst.\n\n## search_results\n\nSecond.\n\n## back_to_search\n\nThird.\n\n" +
			"## theme_toggle\n\nFourth.\n\n## docContent\n\nFifth.\n\n## searchResults\n\nSixth.\n",
		"gamma/journal.md": "---\nname: Journal\ndescription: Gamma journal\nkeywords:\n  - archive\n---\n" +
			"# Journal\n\nThe journal lists orbit anomalies kept in the archive.\n",
	}
}
