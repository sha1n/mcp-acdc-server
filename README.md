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

## Effect on agent work

The design targets four costs that an agent pays when it reads documentation:

- **Retrieval instead of file walking.** An agent that has no index either reads whole files or greps for a literal string. `search` matches stemmed terms with an edit distance of 1 across five fields — source title, path labels, heading path, content, and keywords — so a query does not have to repeat the document's wording.
- **Chunk-level results instead of whole documents.** Search returns the section that matched, not the file that contains it. A section is bounded at every heading and soft-split at 4,000 code points, so a hit costs a section rather than a document. `read` can still fetch the full document when the agent decides it needs it.
- **Citable URIs.** Every result carries a stable `<scheme>://path#fragment` URI and a line range. The agent can quote a source and fetch exactly the same text again later.
- **No restart after an edit.** The catalog and index re-check the content root on `search`, `read`, and resource reads, debounced to once every 2 seconds, and rebuild when the content actually changed. A document edited mid-session is visible to the next call.

The server does not persist an index across restarts, and it does not fetch remote content. It serves what is in the content root at the time of the call.

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

The credential flags (`--auth-basic-username`, `--auth-basic-password`, `--auth-api-keys`) and the four remaining boost factors (`heading`, `title`, `path`, `content`) are listed in the [Configuration Reference](docs/configuration.md).

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
