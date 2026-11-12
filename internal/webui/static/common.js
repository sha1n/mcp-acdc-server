(function () {
  "use strict";

  const THEME_KEY = "acdc-ui-theme";
  const THEME_PREFERENCES = ["system", "light", "dark"];
  const THEME_NAMES = { system: "System", light: "Light", dark: "Dark" };
  const LAST_SEARCH_KEY = "acdc-ui-last-search";
  const LABEL_HUE_COUNT = 10;

  const root = document.documentElement;
  const darkQuery = window.matchMedia("(prefers-color-scheme: dark)");
  let themePreference = readThemePreference();

  function readThemePreference() {
    try {
      const stored = window.localStorage.getItem(THEME_KEY);
      return THEME_PREFERENCES.includes(stored) ? stored : "system";
    } catch (_) {
      return "system";
    }
  }

  function writeThemePreference(preference) {
    try {
      window.localStorage.setItem(THEME_KEY, preference);
    } catch (_) {
      // Storage can be unavailable (private mode, policy); the choice then lasts for this page only.
    }
  }

  function systemTheme() {
    return darkQuery.matches ? "dark" : "light";
  }

  function resolveTheme(preference) {
    return preference === "system" ? systemTheme() : preference;
  }

  function applyTheme() {
    root.dataset.theme = resolveTheme(themePreference);
    root.dataset.themePreference = themePreference;
  }

  // The first step away from "system" always flips the visible theme, so a single click never looks inert.
  function nextThemePreference(preference) {
    const system = systemTheme();
    const opposite = system === "dark" ? "light" : "dark";
    if (preference === "system") return opposite;
    if (preference === opposite) return system;
    return "system";
  }

  function suppressTransitions() {
    root.classList.add("no-transitions");
    window.requestAnimationFrame(() => {
      window.requestAnimationFrame(() => root.classList.remove("no-transitions"));
    });
  }

  function describeThemeToggle(button) {
    const current = themePreference === "system"
      ? `System (${resolveTheme("system")})`
      : THEME_NAMES[themePreference];
    const next = THEME_NAMES[nextThemePreference(themePreference)].toLowerCase();
    const text = `Theme: ${current}. Switch to ${next}.`;
    button.setAttribute("aria-label", text);
    button.title = text;
    const label = button.querySelector(".theme-toggle-label");
    if (label) label.textContent = THEME_NAMES[themePreference];
  }

  function setThemePreference(preference, button) {
    themePreference = preference;
    suppressTransitions();
    applyTheme();
    if (button) describeThemeToggle(button);
  }

  function initThemeToggle() {
    const button = document.getElementById("themeToggle");
    if (!button) return;
    describeThemeToggle(button);
    button.addEventListener("click", () => {
      const preference = nextThemePreference(themePreference);
      writeThemePreference(preference);
      setThemePreference(preference, button);
    });
    darkQuery.addEventListener("change", () => setThemePreference(themePreference, button));
    window.addEventListener("storage", (event) => {
      if (event.key === THEME_KEY) setThemePreference(readThemePreference(), button);
    });
  }

  function labelHue(label) {
    let hash = 0x811c9dc5;
    for (let i = 0; i < label.length; i++) {
      hash ^= label.charCodeAt(i);
      hash = Math.imul(hash, 0x01000193);
    }
    return (hash >>> 0) % LABEL_HUE_COUNT;
  }

  function normaliseLabel(label) {
    return String(label).trim().toLowerCase();
  }

  function el(tag, props, children) {
    const node = document.createElement(tag);
    for (const [key, value] of Object.entries(props || {})) {
      if (value === null || value === undefined || value === false) continue;
      if (key === "className") node.className = value;
      else if (key === "text") node.textContent = value;
      else if (key === "dataset") Object.assign(node.dataset, value);
      else node.setAttribute(key, value === true ? "" : String(value));
    }
    for (const child of [].concat(children || [])) {
      if (child !== null && child !== undefined) node.append(child);
    }
    return node;
  }

  const SVG_NS = "http://www.w3.org/2000/svg";

  function icon(name) {
    const paths = {
      close: "M4 4l8 8M12 4l-8 8",
      chevron: "M4 6l4 4 4-4",
      arrow: "M3 8h10M9 4l4 4-4 4",
      back: "M13 8H3M7 4L3 8l4 4",
      search: "M7 12.5a5.5 5.5 0 1 1 0-11 5.5 5.5 0 0 1 0 11zM11 11l3.5 3.5",
      alert: "M8 5v4M8 11.5v.01M8 1.5l6.5 12h-13z",
      unlink: "M6.5 9.5l-1.5 1.5a2.12 2.12 0 0 1-3-3L3.5 6.5M9.5 6.5l1.5-1.5a2.12 2.12 0 0 1 3 3L12.5 9.5M2 2l12 12",
    };
    const svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("viewBox", "0 0 16 16");
    svg.setAttribute("aria-hidden", "true");
    svg.setAttribute("focusable", "false");
    svg.setAttribute("class", `icon icon-${name}`);
    const path = document.createElementNS(SVG_NS, "path");
    path.setAttribute("d", paths[name]);
    svg.append(path);
    return svg;
  }

  function labelElement(tag, label, props) {
    const node = el(tag, props, label);
    node.classList.add("label");
    node.dataset.label = label;
    node.dataset.hue = String(labelHue(label));
    return node;
  }

  class ApiError extends Error {
    constructor(message, status) {
      super(message);
      this.name = "ApiError";
      this.status = status;
    }
  }

  async function readJSON(response) {
    try {
      return await response.json();
    } catch (_) {
      return null;
    }
  }

  async function api(path, params, signal) {
    const url = new URL(path, window.location.origin);
    if (params) url.search = params.toString();
    let response;
    try {
      response = await fetch(url, { headers: { Accept: "application/json" }, credentials: "same-origin", signal });
    } catch (err) {
      if (err.name === "AbortError") throw err;
      throw new ApiError("The server could not be reached.", 0);
    }
    const body = await readJSON(response);
    if (!response.ok) {
      const reason = body && typeof body.error === "string" ? body.error : `HTTP ${response.status}`;
      throw new ApiError(reason, response.status);
    }
    if (body === null) throw new ApiError("The server returned an unreadable response.", response.status);
    return body;
  }

  function enhanceProse(container) {
    for (const table of container.querySelectorAll("table")) {
      const wrapper = el("div", { className: "table-scroll", tabindex: "0", role: "region", "aria-label": "Table" });
      table.replaceWith(wrapper);
      wrapper.append(table);
    }
    for (const broken of container.querySelectorAll("span.broken-link")) {
      broken.prepend(icon("unlink"));
      broken.append(el("span", { className: "visually-hidden", text: ` (${broken.title || "Broken link"})` }));
    }
    for (const link of container.querySelectorAll('a[target="_blank"]')) {
      link.append(el("span", { className: "visually-hidden", text: " (opens in a new tab)" }));
    }
  }

  function rememberSearch(url) {
    try {
      window.sessionStorage.setItem(LAST_SEARCH_KEY, url);
    } catch (_) {
      // Without session storage the document page falls back to the bare search page.
    }
  }

  function lastSearch() {
    try {
      const url = window.sessionStorage.getItem(LAST_SEARCH_KEY);
      return url && url.startsWith("/ui/") ? url : "/ui/";
    } catch (_) {
      return "/ui/";
    }
  }

  applyTheme();
  document.addEventListener("DOMContentLoaded", initThemeToggle);

  window.ACDC = Object.freeze({
    ApiError,
    api,
    el,
    icon,
    labelElement,
    labelHue,
    normaliseLabel,
    enhanceProse,
    rememberSearch,
    lastSearch,
  });
})();
