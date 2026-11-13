(function () {
  "use strict";

  const { api, el, labelElement, enhanceProse, lastSearch } = window.ACDC;

  const dom = {
    back: document.getElementById("backToSearch"),
    status: document.getElementById("docStatus"),
    retry: document.getElementById("docRetry"),
    errorMessage: document.getElementById("docErrorMessage"),
    missingURI: document.getElementById("docMissingUri"),
    searchLink: document.getElementById("docSearchLink"),
    doc: document.getElementById("docArticle"),
    title: document.getElementById("docTitle"),
    topbarTitle: document.getElementById("topbarTitle"),
    path: document.getElementById("docPath"),
    labels: document.getElementById("docLabels"),
    content: document.getElementById("docContent"),
    announcer: document.getElementById("docAnnouncer"),
  };

  function documentURI() {
    return (new URLSearchParams(window.location.search).get("uri") || "").trim();
  }

  function setState(name) {
    dom.status.dataset.state = name;
    dom.doc.hidden = name !== "ready";
    document.body.dataset.docState = name;
  }

  async function load() {
    const uri = documentURI();
    if (!uri) {
      setState("missing-uri");
      return;
    }
    setState("loading");
    try {
      const doc = await api("/api/v1/document", new URLSearchParams({ uri }));
      render(doc);
      setState("ready");
      scrollToFragment();
      revealTitleOnScroll();
    } catch (err) {
      showFailure(err, uri);
    }
  }

  function showFailure(err, uri) {
    if (err.status === 404) {
      dom.missingURI.textContent = uri;
      dom.searchLink.href = searchURLFor(uri);
      document.title = "Document not found · ACDC";
      setState("not-found");
      return;
    }
    dom.errorMessage.textContent = err.status === 0
      ? "The server could not be reached. Check that it is still running, then try again."
      : `The server answered: ${err.message}.`;
    document.title = "Document unavailable · ACDC";
    setState("error");
  }

  function searchURLFor(uri) {
    const segment = uri.replace(/[?#].*$/, "").split("/").filter(Boolean).pop() || "";
    const term = segment.endsWith(":") ? "" : segment.replace(/\.[a-z0-9]+$/i, "").replace(/[-_]+/g, " ").trim();
    return term ? `/ui/?${new URLSearchParams({ q: term })}` : "/ui/";
  }

  function render(doc) {
    document.title = `${doc.title || doc.path} · ACDC`;
    dom.title.textContent = doc.title || doc.path;
    dom.topbarTitle.textContent = doc.title || doc.path;
    dom.path.textContent = doc.path;
    dom.labels.replaceChildren(...doc.labels.map((label) =>
      labelElement("a", label, { href: `/ui/?${new URLSearchParams({ label })}`, title: `Search label ${label}` })));
    dom.labels.hidden = doc.labels.length === 0;
    dom.content.innerHTML = doc.content_html;
    enhanceProse(dom.content);
    replaceDuplicateTitle(doc.title);
    addHeadingAnchors();
  }

  // Most documents open with an h1 repeating the title; chunk links still target its id,
  // so an empty marker keeps the id without leaving a hidden heading or focus stop behind.
  function replaceDuplicateTitle(title) {
    const first = dom.content.firstElementChild;
    if (!first || first.tagName !== "H1" || first.textContent.trim() !== (title || "").trim()) return;
    if (first.id) first.replaceWith(el("span", { id: first.id, className: "duplicate-title" }));
    else first.remove();
  }

  function addHeadingAnchors() {
    for (const heading of dom.content.querySelectorAll("h1[id], h2[id], h3[id], h4[id], h5[id], h6[id]")) {
      const text = heading.textContent.trim();
      const anchor = el("a", {
        className: "heading-anchor",
        href: `#${encodeURIComponent(heading.id)}`,
        "aria-label": `Link to section ${text}`,
        title: "Copy link to this section",
      }, el("span", { "aria-hidden": "true", text: "#" }));
      anchor.addEventListener("click", () => copyLink(anchor.href));
      heading.append(anchor);
    }
  }

  async function copyLink(url) {
    try {
      await navigator.clipboard.writeText(url);
      announce("Link copied to the clipboard");
    } catch (_) {
      announce("Section link is in the address bar");
    }
  }

  function announce(message) {
    dom.announcer.textContent = "";
    window.setTimeout(() => {
      dom.announcer.textContent = message;
    }, 50);
  }

  function fragmentTarget() {
    const raw = window.location.hash.slice(1);
    if (!raw) return null;
    let id = raw;
    try {
      id = decodeURIComponent(raw);
    } catch (_) {
      // A malformed escape is looked up verbatim.
    }
    return dom.content.querySelector(`[id="${CSS.escape(id)}"]`);
  }

  let currentTarget = null;

  function scrollToFragment() {
    const target = fragmentTarget();
    if (currentTarget) currentTarget.classList.remove("is-target");
    currentTarget = target;
    if (!target) return;
    const anchorTarget = target.classList.contains("duplicate-title") ? dom.doc : target;
    anchorTarget.scrollIntoView({ block: "start" });
    // Reading layout between the class changes restarts the highlight when the same section is targeted again.
    void target.offsetWidth;
    target.classList.add("is-target");
  }

  let titleObserver = null;

  function revealTitleOnScroll() {
    if (titleObserver || !("IntersectionObserver" in window)) return;
    titleObserver = new IntersectionObserver(([entry]) => {
      document.body.classList.toggle("header-passed", !entry.isIntersecting);
    });
    titleObserver.observe(dom.title);
  }

  function start() {
    dom.back.href = lastSearch();
    dom.retry.addEventListener("click", load);
    window.addEventListener("hashchange", scrollToFragment);
    load();
  }

  start();
})();
