'use strict';

// Three deliberately independent result layouts. Each render(root, results, ctx) owns `root`
// and may return a cleanup function for document-level listeners.

App.register('A', 'Column', (root, results, ctx) => {
  const { h } = ctx;
  root.className = 'va';
  let openItem = null;

  const toggle = (item, head, r) => {
    const wasOpen = item === openItem;
    if (openItem) {
      openItem.classList.remove('open');
      openItem.querySelector('.va-head').setAttribute('aria-expanded', 'false');
      openItem.querySelector('.preview')?.remove();
      openItem = null;
    }
    if (wasOpen) return;
    item.classList.add('open');
    head.setAttribute('aria-expanded', 'true');
    item.append(ctx.preview(r, { title: false }));
    openItem = item;
    item.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
  };

  for (const r of results) {
    const sub = [r.path, (r.heading_path || []).join(' › ')].filter(Boolean).join(' · ');
    const head = h('button', { class: 'va-head', type: 'button', 'aria-expanded': 'false' },
      h('div', { class: 'va-title' }, ctx.resultTitle(r)),
      sub ? h('div', { class: 'va-sub' }, sub) : null,
      ctx.snippet(r));
    const item = h('div', { class: 'va-item' }, head);
    head.addEventListener('click', () => toggle(item, head, r));
    root.append(item);
  }
});

App.register('B', 'Split', (root, results, ctx) => {
  const { h } = ctx;
  root.className = 'vb';
  const list = h('div', { class: 'vb-list', role: 'listbox', 'aria-label': 'Results' });
  const detail = h('div', { class: 'vb-detail', 'aria-live': 'polite' });
  root.append(list, detail);
  let selected = -1;

  const rows = results.map((r, i) => {
    const row = h('button', { class: 'vb-row', type: 'button', role: 'option', 'aria-selected': 'false' },
      h('span', { class: 't' }, ctx.resultTitle(r)),
      r.path ? h('span', { class: 'p' }, r.path) : null,
      ctx.snippet(r));
    row.addEventListener('click', () => select(i));
    row.addEventListener('keydown', (e) => {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        select(Math.max(0, Math.min(results.length - 1, i + (e.key === 'ArrowDown' ? 1 : -1))), true);
      }
    });
    list.append(row);
    return row;
  });

  function select(i, focus) {
    if (selected >= 0) { rows[selected].classList.remove('sel'); rows[selected].setAttribute('aria-selected', 'false'); }
    selected = i;
    rows[i].classList.add('sel');
    rows[i].setAttribute('aria-selected', 'true');
    if (focus) rows[i].focus();
    rows[i].scrollIntoView({ block: 'nearest' });
    detail.replaceChildren(ctx.preview(results[i]));
    detail.scrollTop = 0;
  }
  select(0);
});

App.register('C', 'Palette', (root, results, ctx) => {
  const { h } = ctx;
  root.className = 'vc';
  const panel = h('aside', { class: 'vc-over', role: 'dialog', 'aria-modal': 'true', 'aria-label': 'Result preview' });
  const backdrop = h('div', { class: 'vc-backdrop', onclick: () => close() });
  let current = -1;

  const fmtScore = (s) => (typeof s === 'number' ? s.toFixed(2) : '');
  const tbody = h('tbody');
  const rows = results.map((r, i) => {
    const tr = h('tr', { tabindex: '0' },
      h('td', { class: 'num mono' }, i + 1),
      h('td', {}, h('strong', {}, ctx.resultTitle(r)), ctx.snippet(r)),
      h('td', { class: 'mono hide-sm' }, r.path || '', (r.heading_path || []).length ? h('div', {}, r.heading_path.join(' › ')) : null),
      h('td', { class: 'hide-sm' }, h('div', { class: 'lbls' }, (r.labels || []).map((l) => ctx.labelChip(l)))),
      h('td', { class: 'num mono' }, r.start_line ? `${r.start_line}–${r.end_line}` : ''),
      h('td', { class: 'num mono' }, fmtScore(r.score)));
    tr.addEventListener('click', () => open(i));
    tr.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); open(i); }
      else if (e.key === 'ArrowDown') { e.preventDefault(); rows[Math.min(i + 1, rows.length - 1)].focus(); }
      else if (e.key === 'ArrowUp') { e.preventDefault(); rows[Math.max(i - 1, 0)].focus(); }
    });
    tbody.append(tr);
    return tr;
  });

  root.append(
    h('div', { class: 'kbd' }, '↑/↓ to move · Enter to preview · Esc to close'),
    h('table', {},
      h('thead', {}, h('tr', {},
        h('th', { class: 'num' }, '#'), h('th', {}, 'Result'), h('th', { class: 'hide-sm' }, 'Location'),
        h('th', { class: 'hide-sm' }, 'Labels'), h('th', { class: 'num' }, 'Lines'), h('th', { class: 'num' }, 'Score'))),
      tbody),
    backdrop, panel);

  function open(i) {
    if (current >= 0) rows[current].classList.remove('sel');
    current = i;
    rows[i].classList.add('sel');
    panel.replaceChildren(
      h('button', { class: 'ghost-btn vc-close', type: 'button', onclick: () => close() }, 'Esc ✕'),
      ctx.preview(results[i]));
    panel.scrollTop = 0;
    root.classList.add('vc-open');
  }
  function close() {
    if (!root.classList.contains('vc-open')) return;
    root.classList.remove('vc-open');
    if (current >= 0) rows[current].focus();
  }
  const onKey = (e) => { if (e.key === 'Escape') close(); };
  document.addEventListener('keydown', onKey);
  return () => document.removeEventListener('keydown', onKey);
});

App.start();
