(function () {
  "use strict";

  const { api, el, icon, labelElement, labelHue, normaliseLabel, enhanceProse, rememberSearch } = window.ACDC;

  const SEARCH_DEBOUNCE_MS = 250;
  const SUGGEST_DEBOUNCE_MS = 100;
  const SUGGEST_LIMIT = 8;
  const BROWSE_LIMIT = 16;
  const LIMIT_CHOICES = [5, 10, 20, 50, 100];
  const FALLBACK_CONFIG = { default_limit: 20, max_limit: 100 };
  const RESTORE_KEY = "acdc-ui-search-restore";
  const ANNOUNCE_IDLE_MS = 1000;
  const SCORE_STEPS = 4;

  const dom = {
    main: document.getElementById("mainContent"),
    form: document.getElementById("searchForm"),
    searchbox: document.getElementById("searchBox"),
    input: document.getElementById("searchQuery"),
    chips: document.getElementById("searchChips"),
    suggest: document.getElementById("searchSuggest"),
    limit: document.getElementById("searchLimit"),
    status: document.getElementById("searchStatus"),
    notice: document.getElementById("searchNotice"),
    announcer: document.getElementById("searchAnnouncer"),
    browse: document.getElementById("browseLabels"),
    results: document.getElementById("searchResults"),
  };

  const state = { query: "", labels: [], limit: FALLBACK_CONFIG.default_limit };
  let config = FALLBACK_CONFIG;
  let lastResponse = null;
  let searchTimer = 0;
  let searchController = null;
  let suggestTimer = 0;
  let suggestController = null;
  let suggestions = [];
  let suggestionsToken = "";
  let activeSuggestion = -1;
  let pendingRestore = null;
  let announceTimer = 0;
  let started = false;

  // ---------- state & URL ----------

  function hasCriteria() {
    return state.query.trim() !== "" || state.labels.length > 0;
  }

  function isValidLimit(n) {
    return Number.isInteger(n) && n >= 1 && n <= config.max_limit;
  }

  function readURL() {
    const params = new URLSearchParams(window.location.search);
    state.query = params.get("q") || "";
    state.labels = [];
    params.getAll("label").forEach((label) => addUnique(state.labels, label));
    state.limit = limitFromURL();
  }

  // The script is deferred, so the search box accepts text before any handler
  // exists. Only typing can make it differ from the markup by then: its
  // autocomplete="off" keeps browsers from refilling it on reload or history
  // navigation, which would otherwise pass for typed text and override the URL.
  function adoptQueryTypedBeforeScript() {
    if (dom.input.value !== dom.input.defaultValue) state.query = dom.input.value;
  }

  function limitFromURL() {
    const limit = Number.parseInt(new URLSearchParams(window.location.search).get("limit") || "", 10);
    return isValidLimit(limit) ? limit : config.default_limit;
  }

  function addUnique(labels, label) {
    const name = normaliseLabel(label);
    if (name && !labels.includes(name)) labels.push(name);
  }

  function criteriaParams() {
    const params = new URLSearchParams();
    const query = state.query.trim();
    if (query) params.set("q", query);
    state.labels.forEach((label) => params.append("label", label));
    params.set("limit", String(state.limit));
    return params;
  }

  function currentSearchURL() {
    if (hasCriteria()) return `${window.location.pathname}?${criteriaParams()}`;
    if (state.limit === config.default_limit) return window.location.pathname;
    return `${window.location.pathname}?${new URLSearchParams({ limit: String(state.limit) })}`;
  }

  function writeURL() {
    const url = currentSearchURL();
    if (url !== window.location.pathname + window.location.search) {
      window.history.replaceState(null, "", url);
    }
    rememberSearch(url);
  }

  function syncControls() {
    syncCriteriaControls();
    renderLimitOptions();
  }

  function syncCriteriaControls() {
    dom.input.value = state.query;
    renderChips();
    updateMode();
  }

  function updateMode() {
    dom.main.classList.toggle("is-searching", hasCriteria());
  }

  // ---------- limit ----------

  function renderLimitOptions() {
    const values = new Set(LIMIT_CHOICES.filter((n) => n <= config.max_limit));
    values.add(config.default_limit);
    values.add(state.limit);
    const options = [...values].sort((a, b) => a - b).map((n) =>
      el("option", { value: String(n), selected: n === state.limit, text: `${n} results` }));
    dom.limit.replaceChildren(...options);
    dom.limit.value = String(state.limit);
  }

  function onLimitChange() {
    const limit = Number.parseInt(dom.limit.value, 10);
    if (!isValidLimit(limit)) return;
    state.limit = limit;
    searchNow();
  }

  // ---------- chips ----------

  function renderChips() {
    dom.chips.replaceChildren(...state.labels.map(renderChip));
  }

  function renderChip(label) {
    const remove = el("button", { type: "button", className: "chip-remove", "aria-label": `Remove label ${label}` }, icon("close"));
    remove.addEventListener("click", () => {
      removeLabel(label);
      dom.input.focus();
    });
    return el("span", { className: "chip", role: "listitem", dataset: { label, hue: String(labelHue(label)) } }, [
      el("span", { className: "visually-hidden", text: "Label: " }),
      el("span", { className: "chip-text", title: label, text: label }),
      remove,
    ]);
  }

  function addLabel(label) {
    const before = state.labels.length;
    addUnique(state.labels, label);
    if (state.labels.length === before) return;
    renderChips();
    searchNow();
  }

  function removeLabel(label) {
    state.labels = state.labels.filter((l) => l !== label);
    renderChips();
    searchNow();
  }

  function toggleLabel(label) {
    if (state.labels.includes(label)) removeLabel(label);
    else addLabel(label);
  }

  // ---------- suggestions ----------

  function currentToken() {
    const value = dom.input.value;
    if (value === "" || /\s$/.test(value)) return "";
    const parts = value.split(/\s+/);
    return parts[parts.length - 1];
  }

  function isSuggestOpen() {
    return !dom.suggest.hidden && suggestions.length > 0;
  }

  // Closing the list must also cancel any pending or in-flight request, or a late
  // response would reopen it after the user committed or dismissed.
  function cancelSuggestRequest() {
    window.clearTimeout(suggestTimer);
    if (suggestController) suggestController.abort();
    suggestController = null;
  }

  // Options listed for an earlier token must vanish as soon as the token changes, or
  // Tab/Enter could swap the new token for a label it never matched.
  function scheduleSuggest() {
    cancelSuggestRequest();
    const token = currentToken();
    if (token !== suggestionsToken) closeSuggest();
    if (!token) return;
    suggestTimer = window.setTimeout(() => fetchSuggestions(token), SUGGEST_DEBOUNCE_MS);
  }

  async function fetchSuggestions(token) {
    const controller = new AbortController();
    suggestController = controller;
    const params = new URLSearchParams({ q: token, limit: String(SUGGEST_LIMIT + state.labels.length) });
    try {
      const data = await api("/api/v1/labels", params, controller.signal);
      if (controller !== suggestController || currentToken() !== token) return;
      const available = data.labels.filter((l) => !state.labels.includes(l.name)).slice(0, SUGGEST_LIMIT);
      openSuggest(available, token);
    } catch (err) {
      if (err.name !== "AbortError" && controller === suggestController) closeSuggest();
    }
  }

  function openSuggest(items, token) {
    suggestions = items;
    suggestionsToken = token;
    activeSuggestion = -1;
    if (items.length === 0) {
      closeSuggest();
      return;
    }
    dom.suggest.replaceChildren(...items.map(renderSuggestion));
    dom.suggest.hidden = false;
    dom.input.setAttribute("aria-expanded", "true");
    dom.input.removeAttribute("aria-activedescendant");
  }

  function renderSuggestion(item, index) {
    const unit = item.count === 1 ? "chunk" : "chunks";
    const option = el("li", {
      id: `searchOption${index}`,
      role: "option",
      className: "suggest-option",
      "aria-selected": "false",
      dataset: { label: item.name },
    }, [
      el("span", { className: "suggest-swatch", "aria-hidden": "true", dataset: { hue: String(labelHue(item.name)) } }),
      el("span", { className: "suggest-name", text: item.name }),
      el("span", { className: "suggest-count", text: `${item.count} ${unit}` }),
    ]);
    option.addEventListener("mousedown", (event) => event.preventDefault());
    option.addEventListener("mousemove", () => setActiveSuggestion(index));
    option.addEventListener("click", () => acceptSuggestion(index));
    return option;
  }

  function closeSuggest() {
    cancelSuggestRequest();
    suggestions = [];
    suggestionsToken = "";
    activeSuggestion = -1;
    dom.suggest.hidden = true;
    dom.suggest.replaceChildren();
    dom.input.setAttribute("aria-expanded", "false");
    dom.input.removeAttribute("aria-activedescendant");
  }

  function setActiveSuggestion(index) {
    activeSuggestion = index;
    dom.suggest.querySelectorAll('[role="option"]').forEach((option, i) => {
      option.setAttribute("aria-selected", String(i === index));
    });
    const active = document.getElementById(`searchOption${index}`);
    if (active) {
      dom.input.setAttribute("aria-activedescendant", active.id);
      active.scrollIntoView({ block: "nearest" });
    }
  }

  function moveSuggestion(step) {
    const count = suggestions.length;
    const next = activeSuggestion < 0 && step < 0 ? count - 1 : (activeSuggestion + step + count) % count;
    setActiveSuggestion(next);
  }

  function acceptSuggestion(index) {
    const item = suggestions[index];
    const token = currentToken();
    if (!item || token !== suggestionsToken) return;
    const remaining = dom.input.value.slice(0, dom.input.value.length - token.length).trimEnd();
    dom.input.value = remaining ? `${remaining} ` : "";
    state.query = dom.input.value;
    closeSuggest();
    addLabel(item.name);
    dom.input.focus();
  }

  // ---------- input handling ----------

  function onInput() {
    state.query = dom.input.value;
    window.clearTimeout(announceTimer);
    updateMode();
    scheduleSearch(SEARCH_DEBOUNCE_MS);
    scheduleSuggest();
  }

  const keyHandlers = {
    ArrowDown: (event) => navigateSuggest(event, 1),
    ArrowUp: (event) => navigateSuggest(event, -1),
    Enter: onEnter,
    Tab: onTab,
    Escape: onEscape,
    Backspace: onBackspace,
  };

  function onInputKeydown(event) {
    const handler = keyHandlers[event.key];
    if (handler && !event.isComposing) handler(event);
  }

  function navigateSuggest(event, step) {
    if (!isSuggestOpen()) return;
    event.preventDefault();
    moveSuggestion(step);
  }

  function onEnter(event) {
    event.preventDefault();
    if (isSuggestOpen() && activeSuggestion >= 0) {
      acceptSuggestion(activeSuggestion);
      return;
    }
    closeSuggest();
    searchNow();
  }

  function onTab(event) {
    if (!isSuggestOpen() || event.shiftKey) return;
    event.preventDefault();
    acceptSuggestion(activeSuggestion >= 0 ? activeSuggestion : 0);
  }

  function onEscape(event) {
    if (isSuggestOpen()) event.preventDefault();
    closeSuggest();
  }

  function onBackspace(event) {
    if (dom.input.value !== "" || state.labels.length === 0) return;
    event.preventDefault();
    removeLabel(state.labels[state.labels.length - 1]);
  }

  function onGlobalKeydown(event) {
    if (event.key !== "/" || event.metaKey || event.ctrlKey || event.altKey) return;
    const target = event.target;
    if (target.closest && target.closest("input, textarea, select, [contenteditable]")) return;
    event.preventDefault();
    dom.input.focus();
    dom.input.select();
  }

  // ---------- search ----------

  function scheduleSearch(delay) {
    window.clearTimeout(searchTimer);
    searchTimer = window.setTimeout(() => runSearch(false), delay);
  }

  function searchNow() {
    window.clearTimeout(searchTimer);
    runSearch(true);
  }

  // Until startup completes the URL is the only record of a deep-linked limit, and
  // the limit cannot be validated without the config, so nothing may search or
  // rewrite the URL yet. start() runs the search for whatever was entered meanwhile.
  async function runSearch(committed) {
    if (!started) return;
    window.clearTimeout(announceTimer);
    updateMode();
    writeURL();
    if (searchController) searchController.abort();
    if (!hasCriteria()) {
      searchController = null;
      showIdle();
      return;
    }
    const controller = new AbortController();
    searchController = controller;
    showLoading();
    try {
      const data = await api("/api/v1/search", criteriaParams(), controller.signal);
      if (controller === searchController) showResults(data, committed);
    } catch (err) {
      if (err.name !== "AbortError" && controller === searchController) showError(err);
    }
  }

  function setStatus(name, summary, notice) {
    dom.status.dataset.state = name;
    dom.status.replaceChildren(...[].concat(summary || []));
    dom.notice.replaceChildren(...[].concat(notice || []));
    dom.results.setAttribute("aria-busy", String(name === "loading"));
  }

  // Counts from searches fired while typing are announced only once typing pauses, so a
  // screen reader is not interrupted after every debounced keystroke.
  function announce(message, committed) {
    window.clearTimeout(announceTimer);
    dom.announcer.textContent = "";
    announceTimer = window.setTimeout(() => {
      dom.announcer.textContent = message;
    }, committed ? 50 : ANNOUNCE_IDLE_MS);
  }

  function showIdle() {
    lastResponse = null;
    dom.results.replaceChildren();
    setStatus("idle", el("p", { className: "status-hint", "aria-hidden": "true" }, [
      kbd("/"), " to focus", sep(), kbd("↑"), kbd("↓"), " pick a label", sep(), kbd("Enter"), " search", sep(), kbd("Esc"), " dismiss",
    ]));
  }

  function kbd(text) {
    return el("kbd", { text });
  }

  function sep() {
    return el("span", { className: "status-sep", "aria-hidden": "true", text: "·" });
  }

  function showLoading() {
    const spinner = el("span", { className: "status-loading delayed", "aria-hidden": "true" }, [
      el("span", { className: "spinner" }),
      lastResponse ? "Updating results…" : "Searching…",
    ]);
    setStatus("loading", spinner, [...dom.notice.children]);
    if (!lastResponse) dom.results.replaceChildren(...skeletonResults());
  }

  function skeletonResults() {
    return [0, 1, 2].map(() => el("div", { className: "result-skeleton delayed", "aria-hidden": "true" }, [
      el("span", { className: "skeleton-line skeleton-title" }),
      el("span", { className: "skeleton-line skeleton-meta" }),
      el("span", { className: "skeleton-line" }),
      el("span", { className: "skeleton-line skeleton-short" }),
    ]));
  }

  function describeCriteria(response) {
    const parts = [];
    if (response.query) parts.push(" for ", el("q", { className: "status-query", text: response.query }));
    if (response.labels.length > 0) {
      parts.push(response.labels.length === 1 ? " with label " : " with labels ");
      response.labels.forEach((label, i) => {
        if (i > 0) parts.push(" + ");
        parts.push(labelElement("span", label, { className: "label-inline" }));
      });
    }
    return parts;
  }

  function describeCriteriaText(response) {
    const query = response.query ? ` for “${response.query}”` : "";
    const noun = response.labels.length === 1 ? "label" : "labels";
    const labels = response.labels.length > 0 ? ` with ${noun} ${response.labels.join(" and ")}` : "";
    return query + labels;
  }

  function showResults(response, committed) {
    lastResponse = response;
    if (response.results.length === 0) {
      showNoResults(response, committed);
      return;
    }
    const noun = response.total === 1 ? "result" : "results";
    const capped = response.total >= response.limit ? el("span", { className: "status-cap", text: ` · top ${response.limit}` }) : null;
    setStatus("results", el("p", { className: "status-summary" }, [
      el("strong", { text: `${response.total} ${noun}` }), ...describeCriteria(response), capped,
    ]));
    const topScore = Math.max(...response.results.map((r) => r.score));
    dom.results.replaceChildren(...response.results.map((result, index) => renderResult(result, index, topScore)));
    announce(`${response.total} ${noun}${describeCriteriaText(response)}`, committed);
    applyPendingRestore();
  }

  function showNoResults(response, committed) {
    const tips = [];
    if (response.labels.length > 0) {
      const clear = el("button", { type: "button", className: "button button-quiet", text: response.labels.length === 1 ? "Remove the label" : "Clear all labels" });
      clear.addEventListener("click", () => {
        state.labels = [];
        renderChips();
        searchNow();
        dom.input.focus();
      });
      tips.push(el("li", {}, ["Labels combine with AND, so each one narrows the search. ", clear]));
    }
    if (response.query) tips.push(el("li", { text: "Check the spelling, or try fewer or more general words." }));
    setStatus("no-results",
      el("p", { className: "status-summary" }, [el("strong", { text: "No results" }), ...describeCriteria(response)]),
      el("div", { className: "notice" }, [
        icon("search"),
        el("div", { className: "notice-body" }, [
          el("p", { className: "notice-title", text: "Nothing matched" }),
          tips.length > 0 ? el("ul", { className: "notice-tips" }, tips) : null,
        ]),
      ]));
    dom.results.replaceChildren();
    announce(`No results${describeCriteriaText(response)}`, committed);
  }

  function showError(err) {
    lastResponse = null;
    const retry = el("button", { type: "button", className: "button retry-button", text: "Try again" });
    retry.addEventListener("click", () => {
      searchNow();
      dom.input.focus();
    });
    setStatus("error",
      el("p", { className: "status-summary" }, [
        el("strong", { text: "Search failed" }),
        el("span", { className: "status-time", text: ` · ${formatTime(new Date())}` }),
      ]),
      el("div", { className: "notice notice-danger", role: "alert" }, [
        icon("alert"),
        el("div", { className: "notice-body" }, [
          el("p", { className: "notice-title", text: "The search could not be completed" }),
          el("p", { className: "notice-text", text: describeError(err) }),
          el("p", { className: "notice-actions" }, retry),
        ]),
      ]));
    dom.results.replaceChildren();
  }

  function formatTime(date) {
    return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }

  function describeError(err) {
    if (err.status === 0) return "The server could not be reached. Check that it is still running.";
    if (err.status === 401 || err.status === 403) return "The server refused the request. You may need to sign in again.";
    return `The server answered: ${err.message}.`;
  }

  // ---------- results ----------

  function renderResult(result, index, topScore) {
    const article = el("article", { className: "result", dataset: { chunkId: result.chunk_id } });
    const ids = { title: `result${index}Title`, context: `result${index}Context`, meta: `result${index}Meta` };
    const breadcrumb = renderBreadcrumb(result, ids.context);
    const header = el("button", {
      type: "button",
      id: `result${index}Header`,
      className: "result-header",
      "aria-expanded": "false",
      "aria-labelledby": ids.title,
      "aria-describedby": breadcrumb ? `${ids.context} ${ids.meta}` : ids.meta,
    }, [
      el("span", { className: "result-title", id: ids.title, text: result.title || result.path }),
      breadcrumb,
      el("span", { className: "result-meta", id: ids.meta }, [
        el("span", { className: "result-path", text: result.path }),
        renderScore(result.score, topScore),
      ]),
      icon("chevron"),
    ]);
    header.addEventListener("click", () => toggleResult(article, result, index));
    const snippet = el("p", { className: "result-snippet" });
    snippet.innerHTML = result.snippet;
    article.append(el("h2", { className: "result-heading" }, header), snippet, renderResultLabels(result.labels));
    article.addEventListener("keydown", (event) => onResultKeydown(event, article));
    return article;
  }

  function sameHeading(a, b) {
    return String(a || "").trim().toLowerCase() === String(b || "").trim().toLowerCase();
  }

  function renderBreadcrumb(result, id) {
    const path = result.heading_path.slice();
    if (path.length > 0 && sameHeading(path[0], result.title)) path.shift();
    if (path.length === 0) return null;
    const crumbs = [];
    path.forEach((heading, i) => {
      if (i > 0) crumbs.push(el("span", { className: "crumb-sep", "aria-hidden": "true", text: "›" }));
      crumbs.push(el("span", { className: "crumb", text: heading }));
    });
    return el("span", { className: "result-breadcrumb", id }, crumbs);
  }

  function formatScore(score) {
    return score < 0.01 ? "<0.01" : score.toFixed(2);
  }

  function renderScore(score, topScore) {
    if (!(score > 0) || !(topScore > 0)) return null;
    const share = score / topScore;
    const level = Math.max(1, Math.ceil(share * SCORE_STEPS));
    const steps = Array.from({ length: SCORE_STEPS }, (_, i) => el("span", { className: i < level ? "score-step is-on" : "score-step" }));
    return el("span", { className: "result-score", title: `Relevance score ${score}, ${Math.round(share * 100)}% of the top result` }, [
      el("span", { className: "score-meter", "aria-hidden": "true" }, steps),
      `score ${formatScore(score)}`,
      el("span", { className: "visually-hidden", text: `, relevance ${level} of ${SCORE_STEPS}` }),
    ]);
  }

  function renderResultLabels(labels) {
    const buttons = labels.map((label) => {
      const active = state.labels.includes(label);
      const button = labelElement("button", label, {
        type: "button",
        "aria-pressed": String(active),
        title: active ? `Remove label filter ${label}` : `Filter by label ${label}`,
      });
      button.addEventListener("click", () => toggleLabel(label));
      return button;
    });
    return el("div", { className: "result-labels", role: "group", "aria-label": "Labels" }, buttons);
  }

  function toggleResult(article, result, index) {
    const header = article.querySelector(".result-header");
    const wasExpanded = header.getAttribute("aria-expanded") === "true";
    collapseAll();
    if (!wasExpanded) expandResult(article, result, index);
  }

  function collapseAll() {
    for (const article of dom.results.querySelectorAll("article.result.is-expanded")) {
      collapseResult(article);
    }
  }

  function collapseResult(article) {
    const header = article.querySelector(".result-header");
    header.setAttribute("aria-expanded", "false");
    header.removeAttribute("aria-controls");
    article.classList.remove("is-expanded");
    const preview = article.querySelector(".result-preview");
    if (preview) preview.remove();
  }

  function expandResult(article, result, index) {
    const header = article.querySelector(".result-header");
    const preview = renderPreview(result, `result${index}Preview`, header.id);
    article.querySelector(".result-heading").after(preview);
    article.classList.add("is-expanded");
    header.setAttribute("aria-expanded", "true");
    header.setAttribute("aria-controls", preview.id);
    header.scrollIntoView({ block: "nearest" });
  }

  function renderPreview(result, id, headerId) {
    const lines = result.start_line === result.end_line
      ? `Line ${result.start_line}`
      : `Lines ${result.start_line}–${result.end_line}`;
    const content = el("div", { className: "prose prose-compact preview-content" });
    content.innerHTML = result.content_html;
    enhanceProse(content);
    return el("div", { className: "result-preview", id, role: "region", "aria-labelledby": headerId }, [
      el("div", { className: "preview-bar" }, [
        el("a", { className: "preview-title", href: result.document_url, text: result.title || result.path }),
        el("span", { className: "preview-lines", text: lines }),
        el("a", { className: "open-document", href: result.document_url }, ["Open document", icon("arrow")]),
      ]),
      content,
    ]);
  }

  function onResultKeydown(event, article) {
    if (event.key !== "Escape" || !article.classList.contains("is-expanded")) return;
    event.preventDefault();
    collapseResult(article);
    article.querySelector(".result-header").focus();
  }

  // ---------- browse labels (idle) ----------

  async function loadBrowseLabels() {
    try {
      const data = await api("/api/v1/labels", new URLSearchParams({ limit: String(BROWSE_LIMIT) }));
      renderBrowseLabels(data.labels);
    } catch (_) {
      dom.browse.replaceChildren(el("p", { className: "browse-empty", text: "Labels could not be loaded." }));
    }
  }

  function renderBrowseLabels(labels) {
    if (labels.length === 0) {
      dom.browse.replaceChildren(el("p", { className: "browse-empty", text: "This index has no labels yet. Searches still work on text." }));
      return;
    }
    dom.browse.replaceChildren(...labels.map((item) => {
      const button = labelElement("button", item.name, { type: "button", title: `${item.count} chunks` });
      button.append(el("span", { className: "label-count", text: String(item.count) }));
      button.addEventListener("click", () => {
        addLabel(item.name);
        dom.input.focus();
      });
      return button;
    }));
  }

  // ---------- restoring position after Back ----------

  function focusedPlace() {
    const active = document.activeElement;
    if (active === dom.input) return { kind: "input" };
    const header = active && active.closest ? active.closest(".result-header") : null;
    return header ? { kind: "result", chunkId: header.closest("article.result").dataset.chunkId } : null;
  }

  function captureRestorePoint() {
    const expanded = dom.results.querySelector("article.result.is-expanded");
    return {
      url: window.location.pathname + window.location.search,
      scrollY: window.scrollY,
      chunkId: expanded ? expanded.dataset.chunkId : null,
      focus: focusedPlace(),
    };
  }

  function saveRestorePoint() {
    try {
      window.sessionStorage.setItem(RESTORE_KEY, JSON.stringify(captureRestorePoint()));
    } catch (_) {
      // Restoration is a convenience; without storage Back still re-runs the search from the URL.
    }
  }

  function takeRestorePoint() {
    try {
      const point = JSON.parse(window.sessionStorage.getItem(RESTORE_KEY) || "null");
      window.sessionStorage.removeItem(RESTORE_KEY);
      return point && point.url === window.location.pathname + window.location.search ? point : null;
    } catch (_) {
      return null;
    }
  }

  function findResult(chunkId) {
    if (!chunkId) return null;
    return [...dom.results.querySelectorAll("article.result")].find((a) => a.dataset.chunkId === chunkId) || null;
  }

  function restoreFocus(place) {
    if (!place) return;
    const article = place.kind === "result" ? findResult(place.chunkId) : null;
    const target = place.kind === "input" ? dom.input : article && article.querySelector(".result-header");
    if (target) target.focus({ preventScroll: true });
  }

  function applyPendingRestore() {
    const point = pendingRestore;
    pendingRestore = null;
    if (!point) return;
    const article = findResult(point.chunkId);
    if (article) article.querySelector(".result-header").click();
    restoreFocus(point.focus);
    window.scrollTo(0, point.scrollY);
  }

  // A page restored from the back/forward cache still shows its old response; the
  // catalog may have changed since, so the search always runs again from the URL.
  function onPageShow(event) {
    if (!event.persisted || !started) return;
    pendingRestore = takeRestorePoint() || captureRestorePoint();
    readURL();
    syncControls();
    searchNow();
  }

  // ---------- start ----------

  async function loadConfig() {
    try {
      const data = await api("/api/v1/config");
      if (data.default_limit > 0 && data.max_limit >= data.default_limit) config = data;
    } catch (_) {
      // Built-in defaults match the server's own; a real outage surfaces through the search request.
    }
  }

  function bindEvents() {
    dom.form.addEventListener("submit", (event) => {
      event.preventDefault();
      searchNow();
    });
    dom.input.addEventListener("input", onInput);
    dom.input.addEventListener("keydown", onInputKeydown);
    dom.input.addEventListener("blur", closeSuggest);
    dom.searchbox.addEventListener("click", (event) => {
      if (event.target.matches(".searchbox, .searchbox-field, .chips")) dom.input.focus();
    });
    dom.limit.addEventListener("change", onLimitChange);
    document.addEventListener("keydown", onGlobalKeydown);
    window.addEventListener("pagehide", saveRestorePoint);
    window.addEventListener("pageshow", onPageShow);
  }

  async function start() {
    if ("scrollRestoration" in window.history) window.history.scrollRestoration = "manual";
    // The URL is applied to the controls in the same task that binds their handlers,
    // and never again during startup, so input made while the config loads is kept.
    readURL();
    adoptQueryTypedBeforeScript();
    syncCriteriaControls();
    bindEvents();
    await loadConfig();
    state.limit = limitFromURL();
    renderLimitOptions();
    started = true;
    pendingRestore = takeRestorePoint();
    loadBrowseLabels();
    searchNow();
    if (!hasCriteria() && window.matchMedia("(pointer: fine)").matches) dom.input.focus();
  }

  start();
})();
