# ACDC MCP Server Specifications

## Functional Specifications

The ACDC (Agent Content Discovery Companion) MCP Server is designed to serve organization-wide knowledge base resources to AI agents via the Model Context Protocol (MCP). It acts as a bridge between static content repositories and AI agents, providing discovery, search, and retrieval capabilities.

### Core Principles
1.  **Centralized Content**: Operates on a local directory (typically a mounted volume) containing static Markdown resources.
2.  **Zero-Config Client**: Clients discover capabilities dynamically via MCP tool definitions.
3.  **Metadata-Driven**: Server identity, tool exposure, and indexing use built-in defaults, optionally overridden by a `.acdc/config.yaml` manifest in the content root.
4.  **Transport Agnostic**: Supports both `stdio` (local process) and `sse` (HTTP) transports.

---

## Configuration

The server is configured via environment variables, command-line flags, or a `.env` file in the current working directory. CLI flags take precedence over environment variables, which take precedence over `.env` file values.

| Environment Variable | CLI Flag | Description | Default |
| :--- | :--- | :--- | :--- |
| `ACDC_MCP_CONTENT_DIR` | `--content-dir`, `-c` | Root directory to serve; a `.acdc/config.yaml` there is optional (see Content Repository Structure). | current working directory |
| `ACDC_MCP_TRANSPORT` | `--transport`, `-t` | Communication transport: `stdio` or `sse`. | `stdio` |
| `ACDC_MCP_HOST` | `--host`, `-H` | Host interface to bind for SSE transport. | `0.0.0.0` |
| `ACDC_MCP_PORT` | `--port`, `-p` | Port to listen on for SSE transport. | `8080` |
| `ACDC_MCP_SEARCH_MAX_RESULTS` | `--search-max-results`, `-m` | Max results returned by the search tool. | `10` |
| `ACDC_MCP_SEARCH_KEYWORDS_BOOST` | `--search-keywords-boost` | Boost factor for keyword matches. | `3.0` |
| `ACDC_MCP_SEARCH_HEADING_BOOST` | `--search-heading-boost` | Boost factor for heading path (`heading_path`) matches. | `2.5` |
| `ACDC_MCP_SEARCH_TITLE_BOOST` | `--search-title-boost` | Boost factor for document title (`source_title`) matches. | `2.0` |
| `ACDC_MCP_SEARCH_PATH_BOOST` | `--search-path-boost` | Boost factor for path label (`path_labels`) matches. | `1.25` |
| `ACDC_MCP_SEARCH_CONTENT_BOOST` | `--search-content-boost` | Boost factor for content matches. | `1.0` |
| `ACDC_MCP_SEARCH_RESULT_MODE` | `--search-result-mode` | Search output detail: `references` or `content`. | `references` |
| `ACDC_MCP_SEARCH_IN_MEMORY` | `--search-in-memory` | Hold the search index in memory instead of on disk. | `true` |
| `ACDC_MCP_AUTH_TYPE` | `--auth-type`, `-a` | Authentication mode for SSE: `none`, `basic`, `apikey`. | `none` |
| `ACDC_MCP_AUTH_BASIC_USERNAME` | `--auth-basic-username`, `-u` | Username for Basic Auth. | - |
| `ACDC_MCP_AUTH_BASIC_PASSWORD` | `--auth-basic-password`, `-P` | Password for Basic Auth. | - |
| `ACDC_MCP_URI_SCHEME` | `--uri-scheme`, `-s` | URI scheme for resource URIs (RFC 3986 compliant). | `acdc` |
| `ACDC_MCP_AUTH_API_KEYS` | `--auth-api-keys`, `-k` | Comma-separated list of valid API keys for `apikey` auth. | - |

---

## Content Repository Structure

`.acdc/config.yaml` at the root of `ACDC_MCP_CONTENT_DIR` is optional. If it is absent, the server falls back to built-in zero-config defaults (see 0 below) rather than failing startup. If it is present, source documents are discovered in one of two mutually exclusive modes, selected by the presence of an `index` block in `.acdc/config.yaml`:

```text
/ (Content Root)
└── .acdc/
    ├── config.yaml         # Server identity, tool, and index configuration (Optional — see 0. Zero-Config Defaults)
    └── resources/          # Legacy discovery: used when `index` is absent
        ├── guide.md
        └── subfolder/
            └── details.md
```

### 0. Zero-Config Defaults (`.acdc/config.yaml` absent)

-   **Activation**: Triggered only when `.acdc/config.yaml` does not exist at the content root. A manifest that exists but cannot be read, fails to parse as YAML, or fails `Validate()` is still a fatal startup error — only a *missing* file falls back to defaults.
-   **Default index**: Equivalent to a manifest with `index.include: ["README.md", "docs/**/*.md"]` (no `exclude`), which selects the same configured chunk indexing path described in 2b, with the same optional-frontmatter metadata derivation.
-   **Error handling (lenient)**: Unlike an explicit `index` block, the defaulted index tolerates a selection that matches zero files: it is logged as a warning and the server starts with an empty catalog instead of failing. Per-file read, parse, and URI-construction failures are skipped with a warning in both modes, as described in 2b. Configuration-shape errors — an invalid, absolute, or root-escaping glob pattern, a selected non-Markdown file, or a content root that cannot be resolved — remain fatal, exactly as in 2b.
-   **Directory pruning**: `.git` is always skipped during traversal, in both this mode and 2b. Under this defaulted mode only, `node_modules`, `vendor`, `dist`, `build`, `target`, and `.venv` are also skipped, so a repository's dependency and build-output directories are never scanned for Markdown. The content root itself is never pruned, even if its name matches one of these.
-   **Derived identity**: The server name is `"<base> Documentation"`, where `<base>` is the base name of the resolved `--content-dir` path (falling back to `"Repository"` if no meaningful base name can be derived, e.g. the root directory). The version is the build-injected binary version, or `0.0.0` when it is empty. Instructions are generated from a built-in template naming the repository:
    ```text
    Documentation for the <base> repository: guides, references, design documents, specs and plans kept under docs/, plus the top-level README.

    Search here before answering questions about this repository's conventions, architecture, decisions, or planned work. Prefer these documents over assumptions drawn from source code alone.
    ```
-   **Startup log**: When defaults are used, the server logs `.acdc/config.yaml not found, using built-in defaults` at info level, with the derived server name and default index patterns.

### 1. Metadata Manifest (`.acdc/config.yaml`)

Defines the server's identity, optional tool overrides, and optional configured indexing.

**Schema:**
```yaml
server:
  name: <string>        # Display name of the MCP server
  version: <string>     # Semantic version string
  instructions: <string> # Delivered to clients as the MCP server's `instructions` field

tools:                  # Optional: Override default tool descriptions
  - name: search
    description: <string> 
  - name: read
    description: <string> 

index:                  # Optional: switches discovery to configured chunk indexing
  include:               # Required if `index` is present: glob patterns, relative to content root
    - docs/**/*.md
  exclude:               # Optional: glob patterns; always wins over `include`
    - docs/generated/**
```
*Note: If the `tools` section is omitted or a specific tool is not listed, the server provides high-quality default descriptions for the `search` and `read` tools.*

### 2a. Legacy Resource Discovery (`.acdc/resources/`, `index` absent)

-   **Discovery**: The server recursively scans `.acdc/resources/` for `.md` and `.markdown` files. A missing directory yields zero resources (not an error).
-   **URI Scheme**: `<scheme>://<relative_path_without_extension>` (default scheme: `acdc`)
    -   Example: `.acdc/resources/docs/guide.md` -> `acdc://docs/guide`
    -   With `--uri-scheme myorg`: `.acdc/resources/docs/guide.md` -> `myorg://docs/guide`
    -   The scheme must be RFC 3986 compliant (starts with a letter, followed by letters/digits/`+`/`-`/`.`).
    -   Windows backslashes are normalized to forward slashes.
-   **File Format**: Must be Markdown with YAML Frontmatter.
-   **Error handling**: A file with invalid YAML, missing frontmatter, missing `name`/`description`, or a relative path that cannot be constructed into a valid resource URI (e.g. containing an ASCII control character) is skipped with a warning log; it does not fail startup.

**Frontmatter Requirements:**
```markdown
---
name: <string>          # Required: Human-readable title
description: <string>   # Required: Brief summary for listing
keywords:               # Optional: List of keywords for search boosting
  - tag1
  - tag2
---
Markdown content follows...
```

### 2b. Configured Chunk Indexing (`index` present)

-   **Discovery**: Files under the content root matching `index.include` (and not matching `index.exclude`) are indexed, using [doublestar](https://github.com/bmatcuk/doublestar) glob syntax where `**` matches zero or more path segments (`docs/**/*.md` matches both `docs/guide.md` and `docs/api/guide.md`). Patterns must be relative to the content root; absolute or root-escaping patterns fail startup. `exclude` always wins over `include`. `.git` is always skipped during traversal (see also the zero-config-only pruning in 0 above).
-   **URI Scheme**: Same construction as legacy discovery, relative to `--content-dir` instead of `.acdc/resources/`.
-   **File Format**: Must be `.md` or `.markdown`. YAML frontmatter is **optional**; when present it must still be valid.
-   **Metadata derivation**: `name` falls back to the first `#` (H1) heading, `description` to the first paragraph, when frontmatter omits them. `description` is always truncated to 200 Unicode characters, whether it came from frontmatter or the fallback paragraph. `keywords` has no fallback.
-   **Error handling**: Discovery separates how the index was *configured* from what it *found*. Configuration-level failures fail server startup: `index.include` missing/empty, an invalid/absolute/root-escaping glob pattern, zero files matched, a matched file that is not Markdown, or a content root that cannot be resolved (e.g. a broken symlink). Content-level failures never fail startup, in either discovery mode: a matched file that cannot be read, cannot be parsed (e.g. invalid YAML frontmatter), or whose URI cannot be constructed as a valid resource address (e.g. a relative path containing an ASCII control character) is logged at `WARN` with its path relative to the content root and the underlying error, then skipped, while its siblings are still indexed. One malformed document therefore costs one missing resource — never a server that refuses to start, and never a catalog that silently stops updating on the mid-session refresh described in [Content Refresh](#content-refresh), which abandons a whole refresh cycle on any error discovery does report.
-   **Not part of this release**: persistence across restarts. The chunk catalog and search index are rebuilt fully at startup, and rebuilt again mid-session whenever a refresh detects a content change — see [Content Refresh](#content-refresh).

### 3. Chunking and Fragment URIs (Both Modes)

Chunking is not specific to configured indexing: regardless of which discovery mode selected a document, every discovered document — legacy or configured — is split into chunks before indexing, and `search` always returns chunk URIs, never bare document URIs.

-   **Chunk boundaries**: A new chunk starts at *every* heading, of any level — a chunk's content never includes its subsections' content, though its `heading_path` still records the full ancestry (e.g. `["Configuration", "Authentication"]`). Content before the first heading forms its own chunk, addressed with the reserved fragment `document`. A heading whose text has no letters or digits (e.g. `# !!!`) falls back to the fragment `section`.
-   **Soft splitting**: A section exceeding a soft limit of **4,000 Unicode code points** is further split along block boundaries into multiple parts, each still tagged with the section's full heading path.
-   **Fragment URIs**: An unsplit section is `<document-uri>#<heading-slug>` (or `<document-uri>#document` for the pre-heading preamble). A split section's parts are `<document-uri>#<heading-slug>~1`, `~2`, ... — every part, including the first, carries the suffix. Reading the bare `#<heading-slug>` of a split section reconstructs the full section by joining its parts. Duplicate heading text produces de-duplicated slugs (`#overview-1`, `#overview-2`, ...).
-   **Result diversity**: `search` caps results per source document before returning up to `ACDC_MCP_SEARCH_MAX_RESULTS`: 1 chunk per document in `references` mode, 2 in `content` mode. Because the cap discards candidates, the internal candidate window adapts — it starts at `ACDC_MCP_SEARCH_MAX_RESULTS` × 5 and widens ×4 for up to two further retrievals (×20, ×80) while the page is short and candidates remain, so a document with many matching sections does not shorten the page.
-   **Path labels**: Each chunk is also ranked on path labels derived from the document's relative path (split on `/`, `-`, `_`, whitespace, and camelCase boundaries; lowercased; deduplicated), indexed with a 1.25x boost — see `path_labels` below.
-   **Duplicate identity**: Startup fails if two discovered documents or chunks resolve to the same URI or ID — for example, `guide.md` and `guide.markdown` selected side by side both produce the URI `<scheme>://guide`. This applies to both discovery modes.

See the [Authoring Resources Guide](authoring-resources.md#chunking-and-fragment-uris) for full details and the [Repository Docs example](../examples/repository-docs/).

---

## Tools

The server always implements and registers the following MCP tools. Their descriptions can be customized via `.acdc/config.yaml`, but sensible defaults are provided.

### `search`
Performs a full-text search over indexed chunks and returns chunk citations, optionally with the full chunk body.

*   **Input Schema:**
    ```json
    {
      "query": "string (Required) - Natural language or keyword query"
    }
    ```
*   **Behavior:**
    *   Searches against `source_title`, `path_labels`, `heading_path`, `content`, and `keywords` fields, with stemming throughout; fuzzy matching (distance 1) applies to all but `path_labels`, which is matched exactly.
    *   Applies boosting: `keywords` (3.0, configurable), `heading_path` (2.5), `source_title` (2.0, configurable), `path_labels` (1.25), `content` (1.0, configurable).
    *   Applies per-source-document result diversity — see [Chunking and Fragment URIs](#3-chunking-and-fragment-uris-both-modes).
*   **Output:**
    Text summary of results in the format:
    ```text
    Search results for '<query>':

    - **<Source Title>** · [<Chunk URI>](<Chunk URI>)
      - <Source Path> > <Heading Path> · lines <Start>-<End> · score <Score>
      - <Highlighted Snippet>

    <Full chunk body — only when ACDC_MCP_SEARCH_RESULT_MODE=content>
    ...
    ```
    *If no results found, returns a descriptive message.*

### `read`
Retrieves the raw content addressed by a URI: a full source document, a single chunk, or a whole (possibly multi-part) section — see [Chunking and Fragment URIs](#3-chunking-and-fragment-uris-both-modes).

*   **Input Schema:**
    ```json
    {
      "uri": "string (Required) - The resource URI (e.g. acdc://path or acdc://path#fragment)"
    }
    ```
*   **Behavior:**
    *   A base document URI (`<scheme>://path`) returns the full document (frontmatter stripped).
    *   A chunk fragment URI (`<scheme>://path#fragment`) returns that chunk's content only.
    *   A section fragment URI for a split section returns all of its parts joined together.
*   **Output:**
    Raw markdown content of the resolved document, chunk, or section.

---

## MCP Resources

In addition to tools, the server exposes source documents (not individual chunks) directly via the MCP `resources/list` capability.

*   **URI**: Same as the `<scheme>://` document URI used in tools (default scheme: `acdc`).
*   **Name**: From frontmatter `name`, or the first `#` heading when using configured indexing without a `name` field.
*   **Description**: From frontmatter `description`, or the first paragraph when using configured indexing without a `description` field. Always truncated to 200 Unicode characters, whether it came from frontmatter or the fallback paragraph.
*   **MIME Type**: `text/markdown`.
*   Chunk and section URIs (`<scheme>://path#fragment`) are not listed; they are reachable via `read` once obtained from a `search` result.

---

## Content Refresh

The content root is re-checked for changes at the start of every `search` call, every `read` call, and every resource read. `resources/list` does not trigger a check: the underlying SDK serves it directly, with no handler to route through revalidation, so its listing can lag behind the other three surfaces until one of them runs.

A check is debounced to at most once every 2 seconds — a burst of requests inside that window is served from the previous result rather than re-walking the content root. The check itself is layered to keep the common case cheap: it first compares a digest of file paths, sizes, and modification times (no file contents are read) against the last check, and only when that digest changed does it re-discover and diff a content digest, and only when *that* digest also changed does it re-assemble the catalog, rebuild the Bleve search index, publish the new catalog, and reconcile registered resources — sending `notifications/resources/list_changed` to connected clients when the resource set actually changed. An untouched content root costs one cheap filesystem walk per debounce interval; the full rebuild only runs on an actual change.

This applies identically to both discovery modes (legacy `.acdc/resources/` and configured indexing) and to both transports — there is no way to scope refresh to one or disable it. Every error on this path (a failed walk, discovery, parse, or index rebuild) is logged and swallowed: the previously published catalog and index stay in place and the next debounce interval retries from scratch. This is deliberately different from the fatal startup errors listed elsewhere on this page — a malformed document must never take down a server that has already been serving successfully.

A content-change rebuild holds the search index's write lock for its duration, so a concurrent `search` call briefly blocks until the rebuild finishes. For the zero-config, locally-indexed case this is on the order of milliseconds; for a large curated corpus served over SSE to many concurrent clients, it is a real (if brief) pause on every edit, with no configuration to opt out of it. Because the replacement index is built before the previous one is released, a rebuild also holds two complete indexes at once — under the default in-memory mode, that is twice the index's memory for its duration.

Persistence across restarts is still not part of this release: the catalog and index are rebuilt from scratch every time the process starts, independent of the mid-session refresh described here. The index is held in memory by default; under `--search-in-memory=false` (or `ACDC_MCP_SEARCH_IN_MEMORY=false`) it is written to a temporary directory that is discarded on shutdown, so neither mode survives a restart.

---

## Transports

The server supports two transport modes, which are **mutually exclusive**. Only one transport can be active at a time.

### Stdio (Default)
*   **Standard Input**: Receives JSON-RPC messages.
*   **Standard Output**: Sends JSON-RPC responses.
*   **Standard Error**: Structured logs (JSON or text).

### SSE (Server-Sent Events)
Used for remote connections.

*   **GET /sse**: Establishes the event stream.
*   **POST /messages**: Endpoint for client JSON-RPC requests.
*   **GET /health**: Health check (200 OK). Always public.

**Authentication (SSE Only):**
*   **Basic**: Standard `Authorization: Basic <base64>` header.
*   **API Key**: `X-API-Key: <key>` header.
*   *Note: Only `/health` is always public.*

---

## Search Implementation Details

*   **Engine**: Bleve (Go) full-text search engine.
*   **Indexing**: Indexes document chunks (not whole documents), in-memory or in a temporary directory — no persistence across restarts. Built in full at startup, and rebuilt in full again mid-session whenever a debounced refresh detects a content change; see [Content Refresh](#content-refresh) for the trigger, debounce interval, and concurrency cost.
*   **Features**:
    *   **Fuzzy Search**: Matches terms with an edit distance of 1 on four of the five indexed fields; `path_labels` is matched exactly.
    *   **Stemming**: Uses the standard English analyzer for language-aware matching.
    *   **Highlighting**: Generates dynamic snippets with search term context.
    *   **Result Diversity**: Caps results per source document (1 in `references` mode, 2 in `content` mode) so results aren't dominated by a single document, over-fetching candidates — and widening that window when the cap discards them — so the cap does not shorten the page instead.
*   **Indexed Fields (Default Boosts)**:
    *   `chunk_id`, `source_id`, `source_uri`, `chunk_uri`, `source_path` (Stored, Identifier)
    *   `source_title` (Stored, Indexed, Boost x2.0)
    *   `path_labels` (Stored, Indexed, Boost x1.25)
    *   `heading_path` (Stored, Indexed, Boost x2.5)
    *   `content` (Stored, Indexed, Boost x1.0)
    *   `keywords` (Stored, Indexed, Boost x3.0, Optional)
    *   `start_line`, `end_line` (Stored, Numeric)
    *   `section_fragment`, `fragment`, `part`, `part_count` (Stored, indexed via Bleve's dynamic default mapping; not explicitly boosted or queried)