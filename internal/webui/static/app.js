'use strict';

// Shared shell: theme, search box, label chips + autocomplete, URL state, API, and non-result states.
// Result layouts live in variants.js and register themselves via App.register.
const App = (() => {
  const SEARCH_DEBOUNCE_MS = 250;
  const SUGGEST_DEBOUNCE_MS = 120;
  const SUGGEST_LIMIT = 8;
  const THEMES = ['system', 'light', 'dark'];
  const THEME_KEY = 'acdc-ui-theme';
  const LIMIT_CHOICES = [5, 10, 20, 50, 100];

  const layouts = new Map();
  const state = { q: '', labels: [], limit: 20, variant: 'A' };
  let cfg = { default_limit: 20, max_limit: 100 };
  let view = { kind: 'empty' };
  let cleanupLayout = null;

  let searchTimer = 0;
  let searchSeq = 0;
  let searchAbort = null;
  let suggestTimer = 0;
  let suggestSeq = 0;
  let suggestions = [];
  let activeSuggestion = -1;

  const $ = (id) => document.getElementById(id);
  let el = {};

  function h(tag, attrs, ...children) {
    const node = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v == null || v === false) continue;
      if (k === 'class') node.className = v;
      else if (k === 'html') node.innerHTML = v;
      else if (k.startsWith('on')) node.addEventListener(k.slice(2), v);
      else if (k === 'style') node.style.cssText = v;
      else node.setAttribute(k, v === true ? '' : v);
    }
    for (const c of children.flat()) {
      if (c == null || c === false) continue;
      node.append(c instanceof Node ? c : document.createTextNode(String(c)));
    }
    return node;
  }

  function labelHue(name) {
    let hash = 2166136261;
    const s = name.toLowerCase();
    for (let i = 0; i < s.length; i++) {
      hash ^= s.charCodeAt(i);
      hash = Math.imul(hash, 16777619);
    }
    return (hash >>> 0) % 360;
  }

  function labelChip(name, opts = {}) {
    const style = `--h:${labelHue(name)}`;
    if (opts.onRemove) {
      return h('span', { class: 'chip', style },
        name,
        h('button', { class: 'x', type: 'button', 'aria-label': `Remove label ${name}`, title: 'Remove',
          onclick: (e) => { e.stopPropagation(); opts.onRemove(name); } }, '×'));
    }
    if (opts.clickable !== false) {
      return h('button', { class: 'chip', type: 'button', style, title: `Filter by label “${name}”`,
        onclick: (e) => { e.stopPropagation(); addLabel(name); } }, name);
    }
    return h('span', { class: 'chip', style }, name);
  }

  // ---------- theme ----------
  function readTheme() {
    try { return THEMES.includes(localStorage.getItem(THEME_KEY)) ? localStorage.getItem(THEME_KEY) : 'system'; }
    catch { return 'system'; }
  }
  function applyTheme(t) {
    if (t === 'system') delete document.documentElement.dataset.theme;
    else document.documentElement.dataset.theme = t;
    const icon = { system: '◐', light: '☀', dark: '☾' }[t];
    el.theme.textContent = `${icon} ${t}`;
    try { localStorage.setItem(THEME_KEY, t); } catch { /* storage unavailable: theme still applies for this page */ }
  }
  function cycleTheme() {
    const cur = readTheme();
    applyTheme(THEMES[(THEMES.indexOf(cur) + 1) % THEMES.length]);
  }

  // ---------- API ----------
  async function api(path, signal) {
    const res = await fetch(path, { signal, headers: { Accept: 'application/json' } });
    let body = null;
    try { body = await res.json(); } catch { /* non-JSON body handled below */ }
    if (!res.ok) throw new Error((body && body.error) || `HTTP ${res.status}`);
    if (body == null) throw new Error('Invalid JSON response');
    return body;
  }

  // ---------- URL ----------
  function readURL() {
    const p = new URLSearchParams(location.search);
    state.q = p.get('q') || '';
    state.labels = dedupe(p.getAll('label').map((s) => s.trim()).filter(Boolean));
    const lim = parseInt(p.get('limit') || '', 10);
    state.limit = Number.isFinite(lim) ? lim : NaN;
    const v = (p.get('variant') || 'A').toUpperCase();
    state.variant = layouts.has(v) ? v : 'A';
  }
  function writeURL() {
    const p = new URLSearchParams();
    if (state.q.trim()) p.set('q', state.q.trim());
    for (const l of state.labels) p.append('label', l);
    if (state.limit !== cfg.default_limit) p.set('limit', String(state.limit));
    p.set('variant', state.variant);
    history.replaceState(null, '', `${location.pathname}?${p}`);
  }

  function dedupe(list) {
    const seen = new Set();
    return list.filter((l) => { const k = l.toLowerCase(); if (seen.has(k)) return false; seen.add(k); return true; });
  }

  // ---------- chips ----------
  function renderChips() {
    el.chips.replaceChildren(...state.labels.map((l) => labelChip(l, { onRemove: removeLabel })));
  }
  function addLabel(name) {
    if (!state.labels.some((l) => l.toLowerCase() === name.toLowerCase())) {
      state.labels.push(name);
      renderChips();
    }
    searchNow();
  }
  function removeLabel(name) {
    state.labels = state.labels.filter((l) => l !== name);
    renderChips();
    el.input.focus();
    searchNow();
  }

  // ---------- autocomplete ----------
  function currentToken() {
    const v = el.input.value;
    const caret = el.input.selectionStart == null ? v.length : el.input.selectionStart;
    const before = v.slice(0, caret).match(/(\S+)$/);
    if (!before) return null;
    const after = v.slice(caret).match(/^\S*/)[0];
    return { text: before[1] + after, start: caret - before[1].length, end: caret + after.length };
  }
  function scheduleSuggest() {
    clearTimeout(suggestTimer);
    suggestTimer = setTimeout(fetchSuggestions, SUGGEST_DEBOUNCE_MS);
  }
  async function fetchSuggestions() {
    const tok = currentToken();
    const seq = ++suggestSeq;
    if (!tok) { closeSuggest(); return; }
    try {
      const data = await api(`/api/v1/labels?q=${encodeURIComponent(tok.text)}&limit=${SUGGEST_LIMIT + state.labels.length}`);
      if (seq !== suggestSeq) return;
      const chosen = new Set(state.labels.map((l) => l.toLowerCase()));
      suggestions = (data.labels || []).filter((l) => !chosen.has(l.name.toLowerCase())).slice(0, SUGGEST_LIMIT);
      activeSuggestion = -1;
      renderSuggest();
    } catch {
      if (seq === suggestSeq) closeSuggest();
    }
  }
  function renderSuggest() {
    if (!suggestions.length || document.activeElement !== el.input) { closeSuggest(); return; }
    el.suggest.replaceChildren(...suggestions.map((s, i) => h('li', {
      role: 'option', id: `sug-${i}`, class: i === activeSuggestion ? 'active' : '',
      'aria-selected': i === activeSuggestion ? 'true' : 'false',
      onmousedown: (e) => { e.preventDefault(); acceptSuggestion(i); },
    }, labelChip(s.name, { clickable: false }), h('span', { class: 'count' }, `${s.count}`))));
    el.suggest.hidden = false;
    el.input.setAttribute('aria-expanded', 'true');
    if (activeSuggestion >= 0) el.input.setAttribute('aria-activedescendant', `sug-${activeSuggestion}`);
    else el.input.removeAttribute('aria-activedescendant');
  }
  function closeSuggest() {
    suggestSeq++;
    suggestions = [];
    activeSuggestion = -1;
    el.suggest.hidden = true;
    el.suggest.replaceChildren();
    el.input.setAttribute('aria-expanded', 'false');
    el.input.removeAttribute('aria-activedescendant');
  }
  function acceptSuggestion(i) {
    const s = suggestions[i];
    if (!s) return;
    const tok = currentToken();
    if (tok) {
      const v = el.input.value;
      const next = (v.slice(0, tok.start) + v.slice(tok.end)).replace(/\s{2,}/g, ' ');
      el.input.value = next.trimStart();
      const pos = Math.min(tok.start, el.input.value.length);
      el.input.setSelectionRange(pos, pos);
    }
    state.q = el.input.value;
    closeSuggest();
    addLabel(s.name);
  }
  function moveSuggestion(delta) {
    const n = suggestions.length;
    activeSuggestion = activeSuggestion < 0 ? (delta > 0 ? 0 : n - 1) : (activeSuggestion + delta + n) % n;
    renderSuggest();
  }

  function onInputKey(e) {
    const open = !el.suggest.hidden && suggestions.length > 0;
    switch (e.key) {
      case 'ArrowDown': case 'ArrowUp':
        if (open) { e.preventDefault(); moveSuggestion(e.key === 'ArrowDown' ? 1 : -1); }
        break;
      case 'Enter':
        e.preventDefault();
        if (open && activeSuggestion >= 0) acceptSuggestion(activeSuggestion);
        else { closeSuggest(); searchNow(); }
        break;
      case 'Tab':
        if (open) { e.preventDefault(); acceptSuggestion(activeSuggestion >= 0 ? activeSuggestion : 0); }
        break;
      case 'Escape':
        if (open) { e.preventDefault(); closeSuggest(); }
        break;
      case 'Backspace':
        if (el.input.value === '' && state.labels.length) {
          e.preventDefault();
          removeLabel(state.labels[state.labels.length - 1]);
        }
        break;
    }
  }

  // ---------- search ----------
  function scheduleSearch() {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(runSearch, SEARCH_DEBOUNCE_MS);
  }
  function searchNow() {
    clearTimeout(searchTimer);
    runSearch();
  }
  async function runSearch() {
    state.q = el.input.value;
    writeURL();
    const q = state.q.trim();
    if (searchAbort) searchAbort.abort();
    const seq = ++searchSeq;
    if (!q && !state.labels.length) { setStatus(''); show({ kind: 'empty' }); return; }

    const p = new URLSearchParams();
    if (q) p.set('q', q);
    for (const l of state.labels) p.append('label', l);
    p.set('limit', String(state.limit));

    searchAbort = new AbortController();
    setStatus('Searching…');
    if (view.kind !== 'results') show({ kind: 'loading' });
    else el.results.setAttribute('aria-busy', 'true');
    try {
      const data = await api(`/api/v1/search?${p}`, searchAbort.signal);
      if (seq !== searchSeq) return;
      const results = data.results || [];
      setStatus(`${results.length} result${results.length === 1 ? '' : 's'}${results.length >= state.limit ? ` (top ${state.limit})` : ''}`);
      show(results.length ? { kind: 'results', results } : { kind: 'none' });
    } catch (err) {
      if (err.name === 'AbortError' || seq !== searchSeq) return;
      setStatus('Search failed', true);
      show({ kind: 'error', message: err.message });
    } finally {
      if (seq === searchSeq) el.results.removeAttribute('aria-busy');
    }
  }

  function setStatus(text, isError) {
    el.status.textContent = text;
    el.status.classList.toggle('error', !!isError);
  }

  // ---------- views ----------
  function show(next) {
    view = next;
    if (cleanupLayout) { cleanupLayout(); cleanupLayout = null; }
    el.results.replaceChildren();
    switch (view.kind) {
      case 'empty': el.results.append(emptyState()); break;
      case 'loading': el.results.append(h('div', { class: 'state' }, h('div', { class: 'spinner' }), 'Searching…')); break;
      case 'none': el.results.append(h('div', { class: 'state' }, h('h2', {}, 'No results'),
        state.labels.length ? 'Try removing a label filter or broadening the query.' : 'Try different words or a label filter.')); break;
      case 'error': el.results.append(h('div', { class: 'state error' }, h('h2', {}, 'Something went wrong'), view.message,
        h('div', { class: 'chip-row' }, h('button', { class: 'ghost-btn', type: 'button', onclick: searchNow }, 'Retry')))); break;
      case 'results': {
        const root = h('div');
        el.results.append(root);
        cleanupLayout = layouts.get(state.variant).render(root, view.results, ctx) || null;
        break;
      }
    }
  }

  function emptyState() {
    const row = h('div', { class: 'chip-row' });
    const box = h('div', { class: 'state' }, h('h2', {}, 'Search the knowledge base'),
      'Type a query, or start typing a label name to filter by it.', row);
    api(`/api/v1/labels?limit=12`).then((d) => {
      if ((d.labels || []).length) row.replaceChildren(...d.labels.map((l) => labelChip(l.name)));
    }).catch(() => { /* popular labels are decorative; ignore */ });
    return box;
  }

  function resultTitle(r) {
    return r.title || (r.heading_path && r.heading_path[r.heading_path.length - 1]) || r.path || r.chunk_uri || 'Untitled';
  }

  function preview(r, opts = {}) {
    const hp = r.heading_path || [];
    return h('article', { class: 'preview' },
      opts.title === false ? null : h('h2', { class: 'pv-title' }, resultTitle(r)),
      r.path ? h('div', { class: 'pv-path' }, r.path) : null,
      hp.length ? h('nav', { class: 'crumbs', 'aria-label': 'Heading path' }, hp.map((s) => h('span', {}, s))) : null,
      h('div', { class: 'pv-meta' },
        (r.labels || []).length ? h('div', { class: 'pv-labels' }, r.labels.map((l) => labelChip(l))) : null,
        r.start_line ? h('span', {}, `lines ${r.start_line}–${r.end_line}`) : null,
        typeof r.score === 'number' ? h('span', {}, `score ${r.score.toFixed(3)}`) : null),
      h('div', { class: 'pv-body', html: r.content_html || '' }));
  }

  const ctx = {
    h, labelChip, preview, resultTitle, addLabel,
    snippet: (r) => h('div', { class: 'snippet', html: r.snippet || '' }),
  };

  // ---------- variants ----------
  function register(key, name, render) { layouts.set(key, { name, render }); }
  function setVariant(key) {
    state.variant = key;
    el.vlabel.textContent = `${key} (${layouts.get(key).name})`;
    writeURL();
    if (view.kind === 'results') show(view);
  }
  function stepVariant(delta) {
    const keys = [...layouts.keys()];
    setVariant(keys[(keys.indexOf(state.variant) + delta + keys.length) % keys.length]);
  }
  function isEditable(t) {
    return t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT' || t.isContentEditable);
  }

  // ---------- limit ----------
  function buildLimit() {
    const max = Math.max(1, cfg.max_limit | 0);
    if (!Number.isFinite(state.limit) || state.limit < 1) state.limit = cfg.default_limit;
    state.limit = Math.min(state.limit, max);
    const opts = [...new Set([...LIMIT_CHOICES, cfg.default_limit, state.limit])].filter((n) => n >= 1 && n <= max).sort((a, b) => a - b);
    el.limit.replaceChildren(...opts.map((n) => h('option', { value: n, selected: n === state.limit }, n)));
  }

  async function start() {
    el = { theme: $('theme-toggle'), input: $('q'), chips: $('chips'), suggest: $('suggest'), status: $('status'),
      limit: $('limit'), results: $('results'), vlabel: $('variant-label') };

    applyTheme(readTheme());
    el.theme.addEventListener('click', cycleTheme);

    readURL();
    el.input.value = state.q;
    renderChips();
    el.vlabel.textContent = `${state.variant} (${layouts.get(state.variant).name})`;

    el.input.addEventListener('input', () => { scheduleSearch(); scheduleSuggest(); });
    el.input.addEventListener('keydown', onInputKey);
    el.input.addEventListener('blur', () => setTimeout(closeSuggest, 0));
    $('search-box').addEventListener('click', (e) => { if (e.target === e.currentTarget) el.input.focus(); });
    el.limit.addEventListener('change', () => { state.limit = parseInt(el.limit.value, 10); searchNow(); });
    $('variant-prev').addEventListener('click', () => stepVariant(-1));
    $('variant-next').addEventListener('click', () => stepVariant(1));
    document.addEventListener('keydown', (e) => {
      if ((e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return;
      if (isEditable(document.activeElement)) return;
      e.preventDefault();
      stepVariant(e.key === 'ArrowLeft' ? -1 : 1);
    });

    try {
      const c = await api('/api/v1/config');
      if (c.default_limit > 0 && c.max_limit > 0) cfg = c;
    } catch { /* fall back to built-in defaults; search errors will surface on their own */ }
    buildLimit();
    el.input.focus();
    searchNow();
  }

  return { register, start };
})();
