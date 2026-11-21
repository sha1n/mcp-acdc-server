<div align="center">

[![CI](https://github.com/sha1n/mcp-acdc-server/actions/workflows/ci.yml/badge.svg)](https://github.com/sha1n/mcp-acdc-server/actions/workflows/ci.yml)
[![CodeQL](https://github.com/sha1n/mcp-acdc-server/actions/workflows/codeql-analysis.yml/badge.svg)](https://github.com/sha1n/mcp-acdc-server/actions/workflows/codeql-analysis.yml)
[![codecov](https://codecov.io/gh/sha1n/mcp-acdc-server/graph/badge.svg?token=T67S1K956N)](https://codecov.io/gh/sha1n/mcp-acdc-server)
[![Go Version](https://img.shields.io/github/go-mod/go-version/sha1n/mcp-acdc-server)](https://go.dev/)
[![License](https://img.shields.io/github/license/sha1n/mcp-acdc-server)](LICENSE)
[![Docker Image](https://img.shields.io/docker/v/sha1n/mcp-acdc-server?label=docker)](https://hub.docker.com/r/sha1n/mcp-acdc-server)

</div>

# mcp-acdc-server

Agent Content Discovery Companion (ACDC) — a Model Context Protocol (MCP) server that indexes a directory of Markdown documents and serves them to MCP clients over `stdio` or SSE.

The server reads Markdown from a content root, splits each document into heading-scoped chunks, indexes the chunks with [Bleve](https://github.com/blevesearch/bleve), and exposes two tools, a resource listing, and optional prompt templates.

## What it does

**Tools**

| Tool | Input | Returns |
|------|-------|---------|
| `search` | a natural-language or keyword query | ranked chunk citations: source title, chunk URI, source path, heading path, line range, score, and a highlighted snippet. With `--search-result-mode content`, the chunk body is included too. |
| `read` | a URI returned by `search` or a resource listing | the raw Markdown of a document, a single chunk, or a whole multi-part section |

**Resources.** Source documents (not chunks) are listed through the MCP `resources/list` capability, with a `<scheme>://` URI, a name, a description, and the `text/markdown` MIME type.

**Prompts.** Markdown files under `.acdc/prompts/` are exposed as MCP prompt templates with typed arguments. See [Authoring Prompts](docs/authoring-resources.md#authoring-prompts).

**Web UI (optional).** With `--ui-enabled`, the SSE server also serves a read-only browser interface at `/ui/` for searching the same index, filtering by label, and reading rendered documents. It is off by default. For stdio launches, `acdc-mcp browse` prints the URL of a UI served by their shared server, or by `browse` itself in the foreground under the `session` server mode, and opens the browser on it. See [Web UI](docs/web-ui.md).

## Effect on agent work

The design targets four costs that an agent pays when it reads documentation:

- **Retrieval instead of file walking.** An agent that has no index either reads whole files or greps for a literal string. `search` matches stemmed terms with an edit distance of 1 across five fields — source title, path labels, heading path, content, and keywords — so a query does not have to repeat the document's wording.
- **Chunk-level results instead of whole documents.** Search returns the section that matched, not the file that contains it. A section is bounded at every heading and soft-split at 4,000 code points, so a hit costs a section rather than a document. `read` can still fetch the full document when the agent decides it needs it.
- **Citable URIs.** Every result carries a stable `<scheme>://path#fragment` URI and a line range. The agent can quote a source and fetch exactly the same text again later.
- **No restart after an edit.** The catalog and index re-check the content root on `search`, `read`, and resource reads, debounced to once every 2 seconds, and rebuild when the content actually changed. A document edited mid-session becomes visible to a call made once that interval has elapsed (see [Content Refresh](docs/SPECIFICATIONS.md#content-refresh)).

The server does not persist an index across restarts, and it does not fetch remote content. It serves what was in the content root at its latest refresh.

## Installation

**Docker**
```bash
docker pull sha1n/mcp-acdc-server:latest
```

**Homebrew (macOS)**
```bash
brew install sha1n/tap/acdc-mcp
```

Linux and Windows users can download a release archive or use the Docker image. To build from source, see the [Development Guide](docs/development.md).

## Running

**Stdio (default).** The content root defaults to the current working directory:
```bash
acdc-mcp
acdc-mcp --content-dir ./content
```

**SSE**
```bash
acdc-mcp --transport sse --content-dir ./content
```

**Docker.** The image defaults to `stdio`, so serving over HTTP needs `ACDC_MCP_TRANSPORT=sse`. Publishing the port alone is not enough:
```bash
docker run -p 8080:8080 \
  -e ACDC_MCP_TRANSPORT=sse \
  -v $(pwd)/content:/app/content \
  sha1n/mcp-acdc-server:latest
```

**Web UI (SSE only).** Add `--ui-enabled` (or `ACDC_MCP_UI_ENABLED=true`) and open `http://<host>:<port>/ui/`. It shares the port and authentication of `/sse`:
```bash
acdc-mcp --transport sse --ui-enabled --content-dir ./content
```

**Web UI for stdio launches.** On macOS and Linux, `acdc-mcp browse` with the same settings as your MCP launch prints the URL of a UI served by that launch's [shared server](#server-mode), on `127.0.0.1` with no authentication, and opens the browser on it. Add `--no-open` to only print the URL, for scripts and SSH sessions. An open page keeps the shared server alive; once no page is open and no MCP session is attached, the shared server exits after its linger, taking the UI with it. Under `--server-mode session` and on Windows, `browse` builds the index and serves the UI itself on `127.0.0.1` until you press Ctrl+C. On Windows, `browse` prints the URL and does not open a browser:
```bash
acdc-mcp browse --content-dir ./content
acdc-mcp browse --content-dir ./content --no-open
```

**Health check (SSE only).** The SSE server serves an unauthenticated `/health` endpoint that returns `200 OK`. Use it as a Kubernetes liveness or readiness probe:
```yaml
livenessProbe:
  httpGet:
    path: /health
    port: 8080
readinessProbe:
  httpGet:
    path: /health
    port: 8080
```

## Content layout

<details>
<summary><strong>Migrating from a layout older than 0.8.0</strong> — the server refuses to start on it.</summary>

Releases before 0.8.0 used three `mcp-*` names at the content root. A single `.acdc/` directory replaces them:

| Before | After |
|--------|-------|
| `mcp-metadata.yaml` | `.acdc/config.yaml` |
| `mcp-prompts/` | `.acdc/prompts/` |
| `mcp-resources/` | `.acdc/resources/` |

The file format did not change, so a move is enough. Run only the lines that apply, and use `mv` when the content root is not a Git repository:

```bash
mkdir -p .acdc
git mv mcp-metadata.yaml .acdc/config.yaml
git mv mcp-prompts .acdc/prompts
git mv mcp-resources .acdc/resources
```

Index patterns stay as they are: `include` and `exclude` always resolved against the content root, not against the directory that holds the manifest. Do not add a `../` prefix.

`.acdc/` starts with a dot, so a copy step that uses a shell glob (`cp -r src/*`, `COPY ./content/*`) drops it silently. Copy the directory itself. A global gitignore that lists `.acdc/` causes the same class of silent failure — check `git status` after the move.

See [Migrating](docs/authoring-resources.md#migrating) for the exact refusal rules.

</details>

`.acdc/config.yaml` is optional. Without it, the server starts anyway: it indexes `README.md` and `docs/**/*.md`, derives its name from the content directory, and uses built-in instructions. Dependency and build directories (`node_modules`, `vendor`, `dist`, `build`, `target`, `.venv`, `.git`) are skipped.

```bash
acdc-mcp --content-dir /path/to/repository
```

Add `.acdc/config.yaml` to override the defaults. It sets the server identity and instructions, and selects one of two discovery modes:

- With an `index` block, any Markdown matching the `include` globs is indexed. Frontmatter is optional; a missing `name` falls back to the first H1 heading and a missing `description` to the first paragraph.
- Without an `index` block, the server scans `.acdc/resources/` and requires `name` and `description` frontmatter on every file.

Both modes chunk documents the same way. See the [Authoring Resources Guide](docs/authoring-resources.md) for frontmatter, glob syntax, and keyword boosting, and the [specification](docs/SPECIFICATIONS.md) for exact behavior.

## Configuration

Settings come from CLI flags, environment variables, or a `.env` file, in that order of precedence.

Rows marked **SSE** are read only under `--transport sse`. The `stdio` transport ignores them.

| Flag | Environment Variable | Default | Description |
|------|---------------------|---------|-------------|
| `--content-dir`, `-c` | `ACDC_MCP_CONTENT_DIR` | current working directory | Root directory to index and serve. |
| `--transport`, `-t` | `ACDC_MCP_TRANSPORT` | `stdio` | `stdio` for a local child process, `sse` for HTTP. |
| `--uri-scheme`, `-s` | `ACDC_MCP_URI_SCHEME` | `acdc` | Scheme of every resource URI. Must be RFC 3986 compliant. |
| `--cross-ref` | `ACDC_MCP_CROSS_REF` | `false` | Rewrite relative Markdown links into resource URIs. |
| `--search-max-results`, `-m` | `ACDC_MCP_SEARCH_MAX_RESULTS` | `10` | Maximum number of chunks one `search` call returns. |
| `--search-result-mode` | `ACDC_MCP_SEARCH_RESULT_MODE` | `references` | `references` returns citations and a snippet. `content` also returns each chunk body, and raises the per-document cap from 1 chunk to 2. |
| `--search-keywords-boost` | `ACDC_MCP_SEARCH_KEYWORDS_BOOST` | `3.0` | Rank multiplier for a match on frontmatter `keywords`. |
| `--search-in-memory` | `ACDC_MCP_SEARCH_IN_MEMORY` | `true` | Hold the index in memory. Set `false` to write it to a temporary directory instead. Neither mode survives a restart. |
| `--host`, `-H` | `ACDC_MCP_HOST` | `0.0.0.0` | **SSE** — interface to bind. |
| `--port`, `-p` | `ACDC_MCP_PORT` | `8080` | **SSE** — port to listen on. |
| `--auth-type`, `-a` | `ACDC_MCP_AUTH_TYPE` | `none` | **SSE** — `none`, `basic`, or `apikey`. `/health` stays public in every mode. |
| `--ui-enabled` | `ACDC_MCP_UI_ENABLED` | `false` | **SSE** — serve the [web UI](docs/web-ui.md) at `/ui/` and its API at `/api/v1/`. Under `stdio` it is ignored with a warning. |
| `--ui-max-results` | `ACDC_MCP_UI_MAX_RESULTS` | `20` | **SSE** — default number of web UI search results, `1`–`100`. Validated only when the UI is enabled. |

The credential flags (`--auth-basic-username`, `--auth-basic-password`, `--auth-api-keys`) and the four remaining boost factors (`heading`, `title`, `path`, `content`) are listed in the [Configuration Reference](docs/configuration.md).

## Server mode

On macOS and Linux, stdio launches that share a server identity (same settings, binary version and startup content) share one catalog and search index. Each launch is a session proxy that relays its session to a detached shared server, started on demand by the first launch and stopped after the linger (default `30s`) once no session is attached and no web UI page is open. After a shared server crash the session proxy attempts recovery without ending the session, and if no shared server can be used the launch continues as a per-session server. Windows launches and `--transport sse` are unchanged.

| Flag | Environment Variable | Default |
|------|---------------------|---------|
| `--server-mode` | `ACDC_MCP_SERVER_MODE` | `shared`; on Windows every launch behaves as `session` |
| `--shared-server-connect-wait` | `ACDC_MCP_SHARED_SERVER_CONNECT_WAIT` | `5s` |
| `--shared-server-linger` | `ACDC_MCP_SHARED_SERVER_LINGER` | `30s` |

`acdc-mcp browse`, with the same settings as a stdio launch, finds or starts the shared server for their server identity, starts its [web UI](docs/web-ui.md#acdc-mcp-browse) on demand on `127.0.0.1`, prints the URL and opens the browser on it unless `--no-open` is set. Under the `session` server mode, and on Windows, it builds the index and serves the UI itself in the foreground until interrupted, with no shared server; on Windows it prints the URL and does not open a browser. The UI has no authentication, so other local users can read it.

Opt out with `--server-mode session`. See [Server Mode](docs/configuration.md#server-mode) for the processes, files and logs it creates and its known limitations.

## Client registration

Because `--content-dir` defaults to the working directory and `.acdc/config.yaml` is optional, the server can be registered once with no content directory. A client that launches the stdio server from the repository root then gets an index of whichever repository it runs in:

```bash
claude mcp add --scope user --transport stdio acdc -- acdc-mcp
codex mcp add acdc -- acdc-mcp
```

Claude Code uses the current repository as the working directory. Codex launches the server from its own working directory unless the `cwd` key is set on the `[mcp_servers.acdc]` table in `~/.codex/config.toml`.

To pin a fixed content directory:

```bash
claude mcp add --scope user --transport stdio acdc -- acdc-mcp --content-dir $ACDC_MCP_CONTENT_DIR
codex mcp add acdc -- acdc-mcp --content-dir $ACDC_MCP_CONTENT_DIR
```

To connect to an SSE deployment:

```bash
claude mcp add --scope user --transport sse acdc http://<host>:<port>/sse
```

> [!NOTE]
> For an authenticated server, add the `Authorization` or `X-API-Key` header to the client configuration.

Codex reaches remote MCP servers over Streamable HTTP, which this server does not serve. Use the stdio transport with Codex.

## Deployment

- **stdio** runs one process per client, next to a Git-managed content repository. Updates arrive with `git pull`.
- **SSE** runs one shared service. Clients across an organization connect to the same index over HTTP, with optional basic or API-key authentication.

The [examples/](examples/) directory holds working setups:

- [Local Content Demo](examples/docker-local-content/) — direct volume mount for fast iteration.
- [Remote Image Demo](examples/docker-image-content/) — content delivered by an init container.
- [Repository Docs Demo](examples/repository-docs/) — configured chunk indexing over an existing `docs/` directory.

## Development

See the [Development Guide](docs/development.md) for build, test, and contribution instructions.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
