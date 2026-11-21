# Web UI

The SSE server, and a stdio [shared server](configuration.md#server-mode) through [`acdc-mcp browse`](#acdc-mcp-browse), can serve a read-only browser interface over the same index that MCP clients search. People can search the content, filter by label, preview matching sections, and read whole documents rendered from Markdown, without an MCP client.

On the SSE server the web UI is **disabled by default**. A deployment that does not enable it behaves exactly as before: `/ui` and `/api/v1/` are not registered and return `404`.

## Enabling it

| CLI Flag | Environment Variable | `.env` key | Description | Default |
|----------|---------------------|------------|-------------|---------|
| `--ui-enabled` | `ACDC_MCP_UI_ENABLED` | `ui.enabled` | Serve the web UI and its JSON API on the SSE server | `false` |
| `--ui-max-results` | `ACDC_MCP_UI_MAX_RESULTS` | `ui.max_results` | Number of search results the page requests when no limit is chosen. Must be between `1` and `100` | `20` |

`--ui-max-results` is validated only when the UI is enabled: an out-of-range value fails startup with `ui.max_results must be between 1 and 100`, but the same value is ignored while the UI is off. It is independent of `--search-max-results`, which limits the MCP `search` tool only.

```bash
acdc-mcp --transport sse --ui-enabled --content-dir ./content
```

```bash
docker run -p 8080:8080 \
  -e ACDC_MCP_TRANSPORT=sse \
  -e ACDC_MCP_UI_ENABLED=true \
  -v $(pwd)/content:/app/content \
  sha1n/mcp-acdc-server:latest
```

Then open `http://<host>:<port>/ui/`.

### SSE transport only

The UI is served by the SSE server, on the same host, port, and listener as `/sse`. There is no separate port. Under `--transport stdio` there is no HTTP server, so `--ui-enabled` has no effect; the server starts normally and logs one warning:

```
level=WARN msg="Web UI requires the sse transport; ui.enabled is ignored"
```

To get the UI for stdio launches, use [`acdc-mcp browse`](#acdc-mcp-browse).

## `acdc-mcp browse`

`acdc-mcp browse` prints the URL of a web UI over the content that stdio launches with the same settings are served from. It takes the same settings as an MCP launch (flags, `.env`, `ACDC_MCP_*`) and computes the [server identity](configuration.md#server-identity) from them, so the same settings reach the same [shared server](configuration.md#server-mode). It finds that shared server, or starts one with the same discovery and connect wait as a session proxy, and waits for its index build with no time limit. The shared server starts its UI on the first `browse` request and keeps it for the rest of its life; every later `browse` gets the same URL. `browse` prints the URL on stdout, then opens the system browser on it (`open` on macOS, `xdg-open` on Linux and the BSDs) and logs to stderr; with a shared server it then exits `0` right away (for the foreground path, see [Without a shared server](#without-a-shared-server)). `--no-open` only prints the URL, for scripts and SSH sessions. When the browser cannot be opened (no opener on the `PATH`, a headless machine, or an opener error), `browse` logs a warning on stderr and carries on as if it had opened; open the printed URL yourself. On Windows, `browse` has no opener: it prints the URL, logs a `Failed to open the browser` warning on stderr unless `--no-open` is set, and does not open a browser.

```bash
acdc-mcp browse --content-dir ./content
# http://127.0.0.1:54321/ui/, opened in the browser
acdc-mcp browse --content-dir ./content --no-open
# http://127.0.0.1:54321/ui/
```

- **Listener.** The UI listens on `127.0.0.1` on an ephemeral port, separate from any MCP traffic, and serves only `/ui/` and `/api/v1/`: there is no `/sse` and no `/health`. It applies no authentication.
- **`ui.max_results`.** The value sent by the `browse` that started the UI applies for the life of that shared server; values sent by later requests are ignored. It is validated on every `browse`, whether or not `ui.enabled` is set.
- **Lifetime.** A `browse` connection is not an attached session, but while it waits for its reply it holds off the [linger](configuration.md#detached-shared-server-process) like one, and once the URL is returned it is closed and, if the shared server is otherwise idle (no attached session and no live viewer), the linger restarts, as when the last session detaches. So even a cold start whose build outlasts the linger leaves the page it opened a whole linger to send its first heartbeat. Every open page is a [viewer](../GLOSSARY.md): it sends a [heartbeat](#post-apiv1viewersheartbeat) when it loads and about every 30 seconds after, and stays live for 2 minutes after its last one. The linger counts down only while the shared server has no attached session and no live viewer, and restarts from when the last of them goes away, so a page that `browse` opened keeps working for as long as it stays open, even with no session attached. The UI ends with its shared server, so once every page is closed (and has stopped being live) and no session is attached, the shared server exits when the linger runs out, and the URL changes whenever the shared server is replaced. The shared server's log records `Web UI started` with the URL.
- **Ignored settings.** `--ui-enabled`, `--host`, `--port` and the authentication settings (`auth.*`) are accepted and ignored, and are not passed to a shared server that `browse` starts.

### Without a shared server

When the effective server mode is `session` (`--server-mode session`, or any launch on Windows), there is no shared server to serve the UI, so `browse` serves it itself in the foreground. It builds the index, serves the UI on `127.0.0.1` on an ephemeral port, prints the URL on stdout and opens the browser on it unless `--no-open` is set, as above (on Windows it only prints the URL). It then runs until it gets `SIGINT` (Ctrl+C) or `SIGTERM`, and exits `0`; the URL stops working when it exits. The listener, `ui.max_results` and ignored settings are as above. It starts no shared server and writes no endpoint files, and pages open on it are not tied to any MCP session.

```bash
acdc-mcp browse --content-dir ./content --server-mode session
# http://127.0.0.1:54321/ui/, opened in the browser; Ctrl+C to stop
```

A requested UI is a required UI. `browse` prints `Error: ...` and exits `1`, and a `browse` that uses a shared server never falls back to serving the UI itself, when:

- the UI cannot bind; on a shared server, other attached sessions are unaffected, and the next `browse` tries again;
- no shared server can be reached or started within the connect wait (shared server mode only);
- the index build fails;
- `--transport sse` is set.

> [!CAUTION]
> The UI started by `browse` has no authentication. Loopback is its only protection, so other users on the same machine can read the content through it while it runs.

## Authentication

`/ui` and `/api/v1/` sit behind the same authentication middleware as `/sse`. `/health` stays public and unchanged in every mode.

| `--auth-type` | Effect on the web UI |
|---------------|----------------------|
| `none` | Open to anyone who can reach the port. |
| `basic` | Unauthenticated requests get `401` with `WWW-Authenticate: Basic realm="Restricted"`, so the browser shows its native login prompt. The page then reuses the credentials for its API calls. |
| `apikey` | A browser cannot attach the `X-API-Key` header, so direct visits get `401`. Put the server behind a reverse proxy that authenticates users and injects `X-API-Key` on requests to `/ui` and `/api/v1/`. |

There is no login page and no session; the UI relies entirely on what the browser or a proxy sends.

## Using the page

**Search page — `/ui/`.** Type a query to search: results update once typing pauses for a quarter of a second, and `Enter` searches immediately. As you type, labels that start with the current word are suggested alongside. Suggestions are optional and never hold back the text search; pick one with the arrow keys and `Enter`, or with `Tab`, to add it as a filter chip, and press `Backspace` in an empty box to remove the last one. Before anything is typed, the page lists the most common labels for browsing. Each result is one chunk — a section of a document — with its title, path, heading path, labels, and a highlighted snippet. Expanding a result previews the chunk's rendered Markdown and links to the full document at that section. A result-count selector offers 5, 10, 20, 50, and 100, plus the configured default if it is not one of those.

Unlike the MCP `search` tool, the web UI does not cap results per document: several sections of the same document can appear in one result list.

The search state lives in the URL, so a search can be bookmarked or shared:

```
/ui/?q=deployment&label=guides&label=docker&limit=50
```

**Document page — `/ui/doc?uri=<source URI>`.** Renders a whole source document. Headings carry ids, so `/ui/doc?uri=acdc%3A%2F%2Fguides%2Fsetup#installation` opens at that heading, and search results link straight to the section they matched. The content is exactly what the MCP `read` tool returns for the same URI, so `--cross-ref` applies here too. Clicking a label on this page starts a label search.

**Theme.** A toggle switches between the system theme, light, and dark. The choice is stored in the browser's local storage.

## Labels

A chunk's labels are the union of its document's [path labels](authoring-resources.md#path-labels) and its frontmatter `keywords`, lower-cased and de-duplicated. A document at `guides/docker-setup.md` with `keywords: [Containers]` carries `guides`, `docker`, `setup`, and `containers`.

- Label filters are exact, case-insensitive matches. They are not stemmed and not fuzzy.
- Several label filters are combined with **AND**: a result must carry every selected label.
- A label filter narrows a text search without changing its scores or its order.
- Labels without a text query list every chunk carrying all selected labels, in document order (by source URI, then line), with a score of `0`.
- Label counts are numbers of chunks, not documents.

## What is rendered, and what is not

Content is rendered on the server with GitHub-flavored Markdown (tables, task lists, strikethrough, autolinks). The rules below are deliberate and hold for both the document page and result previews, except where noted.

**Raw HTML is never rendered.** HTML blocks and inline HTML in Markdown are dropped from the output. A `<script>` or `<img onerror=…>` in a document produces nothing.

**Snippets are escaped plain text.** A snippet is cut from the chunk's text with Markdown syntax removed (heading markers, emphasis, backticks, link destinations, code fences, table pipes). Separators keep the structure readable: ` — ` between blocks (a heading and the paragraph after it, consecutive paragraphs, list items), ` · ` between table cells, and ` / ` between table rows; lines within a block are joined by single spaces. The only markup it contains is `<mark>` around matched terms. Raw HTML in the source stays visible as escaped text: a document that literally contains `<mark>x</mark>` shows those characters, and they are not highlighted.

**Links** are rewritten by target:

| Link target | Rendered as |
|-------------|-------------|
| `#fragment` | On the document page, an in-page link, unchanged. In a result preview (and the search API's `content_html`), a link to that section of the source document: `/ui/doc?uri=<source URI>#fragment`. |
| `http://…`, `https://…`, `www.…` | A link that opens in a new tab with `rel="noopener noreferrer"`. |
| A relative path or a resource URI that resolves to a document in the catalog | A link to that document's page, keeping any `#fragment`. A relative path with percent-encoded characters is still a relative path: `notes%3Av2.md` resolves to the catalog document `notes:v2.md`, not to a URI with scheme `notes`. |
| `mailto:…` and bare e-mail addresses | Plain text — valid, but not navigable from a read-only viewer. |
| Anything else: a missing document, an absolute path (`/…`), a path containing `?`, a path that climbs out of the content root, or another scheme (`javascript:`, `data:`, `ftp:`, …) | The link text, marked as a broken link, with the target in its tooltip. |

**Images are never loaded.** Every Markdown image is shown as a placeholder with its alt text (or `image` when there is none) and the image path in its tooltip. This keeps the page from making requests outside the server, and the server does not serve files from the content root, so relative image paths would not resolve anyway.

## Network and browser security

- The page, its scripts, styles, and icon are embedded in the binary. The browser loads nothing from any other origin, and the page's only requests are to `/api/v1/` on the same server, including the [viewer heartbeat](#post-apiv1viewersheartbeat) every 30 seconds. A heartbeat that fails is ignored silently. External links are followed only when clicked.
- Every page response, including scripts, styles, and the icon, carries:
  - `Content-Security-Policy: default-src 'self'; img-src 'self'; style-src 'self'; script-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`
  - `X-Content-Type-Options: nosniff`
  - `Referrer-Policy: no-referrer`
  - `Cache-Control: no-cache`, so the browser revalidates pages and assets after the server is upgraded
- The UI is read-only. Nothing in it can change content, the index, or configuration.

> [!CAUTION]
> With `--auth-type none`, the web UI and its JSON API are readable by anyone who can reach the listener; the default `--host 0.0.0.0` binds every interface. Binding to `127.0.0.1` is not enough on its own: the server does not validate the `Host` header, so a malicious web page open in a local browser can read the content through DNS rebinding. Use `basic` or `apikey` authentication whenever the UI is enabled anywhere other than a trusted, single-user machine.

## Routes

| Route | Methods | Response |
|-------|---------|----------|
| `/ui` | `GET`, `HEAD` | `301` to `/ui/`, keeping the query string |
| `/ui/` | `GET`, `HEAD` | Search page |
| `/ui/doc` | `GET`, `HEAD` | Document page. Always `200`; the page itself reports a missing or unknown `uri`. |
| `/ui/<asset>` | `GET`, `HEAD` | Embedded script, stylesheet, or icon. Unknown names return `404`; directories are never listed. |
| `/api/v1/config` | `GET` | UI limits |
| `/api/v1/labels` | `GET` | Label suggestions with counts |
| `/api/v1/search` | `GET` | Search results |
| `/api/v1/document` | `GET` | One source document |
| `/api/v1/viewers/heartbeat` | `POST` | `204`; marks the page as a live viewer |

Any other method on a `/ui` route returns `405` with `Allow: GET, HEAD`.

## HTTP API

The page is built on a small JSON API under `/api/v1/`. It is the web UI's backend: it is served only when the UI is enabled, under the same authentication.

Every response, success or error, has `Content-Type: application/json; charset=utf-8`, `Cache-Control: no-store`, and `X-Content-Type-Options: nosniff`, except the heartbeat's bodiless `204`, which carries only `Cache-Control: no-store`. Array fields are always arrays, never `null`.

**Errors** have one shape:

```json
{"error": "limit must be an integer between 1 and 100"}
```

| Status | `error` | When |
|--------|---------|------|
| `400` | `limit must be an integer between 1 and 100` | `limit` is not an integer, or is outside `1`–`100` |
| `400` | `uri is required` | `/api/v1/document` without a non-blank `uri` |
| `400` | `viewer_id must be 1 to 64 letters, digits, '-' or '_'` | Heartbeat body is not exactly one JSON object, or its `viewer_id` is invalid |
| `404` | `not found` | Unknown path under `/api/v1/` (checked before the method) |
| `404` | `document not found` | `uri` is not a source document in the catalog |
| `405` | `method not allowed` | Any method other than `GET` (`POST` for the heartbeat); the response carries `Allow: GET` (`Allow: POST`) |
| `413` | `request body too large` | Heartbeat body over 1 KiB |
| `415` | `content type must be application/json` | Heartbeat without `Content-Type: application/json` |
| `500` | `internal server error` | Any other failure. The cause is logged on the server and never returned. |

### `GET /api/v1/config`

```json
{"default_limit": 20, "max_limit": 100}
```

`default_limit` is `--ui-max-results`; `max_limit` is the fixed upper bound for any `limit` parameter.

### `GET /api/v1/labels`

| Parameter | Description |
|-----------|-------------|
| `q` | Optional label prefix, trimmed and matched case-insensitively. Empty lists all labels. |
| `limit` | `1`–`100`. Default `10`. |

```json
{"labels": [{"name": "reference", "count": 15}, {"name": "guides", "count": 13}]}
```

Ordered by `count` descending, then `name` ascending. `count` is the number of chunks that carry the label.

### `GET /api/v1/search`

| Parameter | Description |
|-----------|-------------|
| `q` | Text query, trimmed. Matched the same way as the MCP `search` tool. |
| `label` | Label filter. Repeatable; values are trimmed and lower-cased, empty values are dropped, duplicates removed. All given labels must match. |
| `limit` | `1`–`100`. Default `--ui-max-results`. |

With neither `q` nor a `label`, the response is `200` with an empty `results` array. With a query, results are ordered by score; with labels only, by source URI and line.

```json
{
  "query": "configuration",
  "labels": [],
  "limit": 2,
  "total": 2,
  "results": [
    {
      "chunk_id": "acdc://reference/configuration#configuration-reference",
      "source_uri": "acdc://reference/configuration",
      "chunk_uri": "acdc://reference/configuration#configuration-reference",
      "title": "Configuration Reference",
      "path": "reference/configuration.md",
      "heading_path": ["Configuration Reference"],
      "labels": ["reference", "configuration", "config", "environment", "flags", "transport", "authentication", "uri-scheme"],
      "start_line": 7,
      "end_line": 9,
      "score": 1.0332335050964987,
      "document_url": "/ui/doc?uri=acdc%3A%2F%2Freference%2Fconfiguration#configuration-reference",
      "snippet": "<mark>Configuration</mark> Reference — The ACDC server can be <mark>configured</mark> through CLI flags, environment variables, or a .env file.",
      "content": "# Configuration Reference\n\nThe ACDC server can be configured…",
      "content_html": "<h1>Configuration Reference</h1>\n<p>The ACDC server can be configured…</p>\n"
    },
    {
      "chunk_id": "acdc://reference/configuration#cli-flags",
      "source_uri": "acdc://reference/configuration",
      "chunk_uri": "acdc://reference/configuration#cli-flags",
      "title": "Configuration Reference",
      "path": "reference/configuration.md",
      "heading_path": ["Configuration Reference", "CLI Flags"],
      "labels": ["reference", "configuration", "config", "environment", "flags", "transport", "authentication", "uri-scheme"],
      "start_line": 11,
      "end_line": 22,
      "score": 0.6020221575542051,
      "document_url": "/ui/doc?uri=acdc%3A%2F%2Freference%2Fconfiguration#cli-flags",
      "snippet": "CLI Flags — Flag · Env Variable · Default · Description / --content-dir · ACDC_MCP_CONTENT_DIR · . · Path to the content directory / --transport · ACDC_MCP_TRANSPORT · stdio · Transport protocol:…",
      "content": "## CLI Flags\n\n| Flag | Env Variable | Default | Description |\n…",
      "content_html": "<h2>CLI Flags</h2>\n<table>\n<thead>\n…"
    }
  ]
}
```

| Field | Description |
|-------|-------------|
| `query`, `labels`, `limit` | The request as the server applied it, after trimming, normalization, and defaults |
| `total` | Number of entries in `results` — not the number of matches in the index |
| `chunk_id`, `chunk_uri` | The chunk's resource URI, readable with the MCP `read` tool |
| `source_uri`, `title`, `path` | The document the chunk belongs to |
| `heading_path` | Headings enclosing the chunk, outermost first |
| `labels` | The chunk's labels |
| `start_line`, `end_line` | Line range of the chunk in the source file |
| `score` | Relevance score; `0` for a label-only listing |
| `document_url` | Document page URL, anchored at the chunk's section heading. No anchor for text that precedes the first heading. |
| `snippet` | Up to 200 characters of the chunk's plain text around its densest run of matches, HTML-escaped, with matches wrapped in `<mark>`. Cuts fall between words, and `…` marks text omitted before or after. Without a text match (a label-only listing), the start of the chunk's text, or the document title when the chunk has no text. |
| `content` | The chunk's Markdown |
| `content_html` | `content` rendered under the rules in [What is rendered](#what-is-rendered-and-what-is-not), without heading ids |

### `GET /api/v1/document`

| Parameter | Description |
|-----------|-------------|
| `uri` | Required. A source document URI, e.g. `acdc://guides/setup`. Chunk URIs (with a `#fragment`) are not documents and return `404`. |

```json
{
  "uri": "acdc://guides/setup",
  "title": "Setup",
  "path": "guides/setup.md",
  "labels": ["guides", "setup"],
  "content": "# Setup\n\n…",
  "content_html": "<h1 id=\"setup\">Setup</h1>\n…"
}
```

`content` is what the MCP `read` tool returns for the URI. `content_html` renders it with an `id` on every heading; the `document_url` of each search result points at one of these ids.

### `POST /api/v1/viewers/heartbeat`

Sent by every page when it loads and about every 30 seconds while it stays open. The body must be JSON with `Content-Type: application/json`:

```json
{"viewer_id": "3f2b8c1e-7a4d-4f0e-9b6a-2c5d8e1f0a7b"}
```

`viewer_id` is 1 to 64 letters, digits, `-` or `_`, chosen once per page load. The body is at most 1 KiB and holds that one object, with nothing but whitespace after it. The response is `204 No Content` with `Cache-Control: no-store`. On a shared server the heartbeat makes that page a live viewer for 2 minutes, which holds off the [linger](#acdc-mcp-browse); on the SSE server it is accepted and has no effect. Requiring a JSON content type means a page on another origin cannot send it without a CORS preflight, which the API never grants.
