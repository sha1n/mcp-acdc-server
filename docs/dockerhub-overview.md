# mcp-acdc-server

**Agent Content Discovery Companion (ACDC) MCP Server**

A Model Context Protocol (MCP) server that gives AI agents searchable access to your Markdown content. Full-text search powered by [Bleve](https://github.com/blevesearch/bleve), dual transport (stdio/SSE), and optional authentication.

📦 **Source & full docs:** [github.com/sha1n/mcp-acdc-server](https://github.com/sha1n/mcp-acdc-server)

---

## Quick Start

> **Important:** The image defaults to the **`stdio`** transport. To run it as an HTTP service you must set `ACDC_MCP_TRANSPORT=sse` — publishing port 8080 alone is not enough.

**SSE (HTTP service):**

```bash
docker run -d --name acdc \
  -p 8080:8080 \
  -e ACDC_MCP_TRANSPORT=sse \
  -v "$(pwd)/content:/app/content:ro" \
  sha1n/mcp-acdc-server:latest

curl http://localhost:8080/health   # -> 200 OK
```

**stdio (agent launches the process):**

```bash
docker run -i --rm \
  -v "$(pwd)/content:/app/content:ro" \
  sha1n/mcp-acdc-server:latest
```

> **Important:** Since 0.8.0, the on-disk content convention moved from `mcp-metadata.yaml`, `mcp-prompts/`, and `mcp-resources/` to a single `.acdc/` directory; the server refuses to start on the old layout. See [Migrating](https://github.com/sha1n/mcp-acdc-server/blob/master/docs/authoring-resources.md#migrating).

### Zero-config content

No manifest required. Mount any repository and the server indexes `README.md` and `docs/**/*.md` using built-in server identity and instructions:

```bash
docker run -d -p 8080:8080 -e ACDC_MCP_TRANSPORT=sse \
  -v "$(pwd):/app/content:ro" sha1n/mcp-acdc-server:latest
```

Add a `.acdc/config.yaml` to the content root to override the defaults — customize server identity and instructions, and add an `index` block to select Markdown by glob pattern. See the [Authoring Resources Guide](https://github.com/sha1n/mcp-acdc-server/blob/master/docs/authoring-resources.md).

---

## Image Details

| | |
|---|---|
| **Platforms** | `linux/amd64`, `linux/arm64` |
| **Base** | `alpine` |
| **Exposed port** | `8080` |
| **Working directory** | `/app` |
| **Content mount point** | `/app/content` |

Built-in environment defaults: `ACDC_MCP_CONTENT_DIR=/app/content`, `ACDC_MCP_HOST=0.0.0.0`, `ACDC_MCP_PORT=8080`.

### Tags

| Tag | Meaning |
|---|---|
| `latest` | Latest build of the default branch |
| `0.7.0`, `0.7` | Semver release tags — pin these for reproducible deployments |
| `master` | Default-branch builds |
| `sha-<short>` | Immutable per-commit builds |

---

## Configuration

Every setting is available as a CLI flag or an `ACDC_MCP_*` environment variable. Environment variables are the natural fit for containers.

### Server

| Environment Variable | Description | Default |
|---|---|---|
| `ACDC_MCP_CONTENT_DIR` | Path to the content directory | `/app/content` (in image) |
| `ACDC_MCP_TRANSPORT` | `stdio` or `sse` | `stdio` |
| `ACDC_MCP_HOST` | Bind host (SSE only) | `0.0.0.0` |
| `ACDC_MCP_PORT` | Bind port (SSE only) | `8080` |
| `ACDC_MCP_URI_SCHEME` | Resource URI scheme, e.g. `acdc://guides/setup` | `acdc` |
| `ACDC_MCP_CROSS_REF` | Rewrite relative Markdown links into resource URIs | `false` |

### Search

| Environment Variable | Description | Default |
|---|---|---|
| `ACDC_MCP_SEARCH_MAX_RESULTS` | Maximum results returned | `10` |
| `ACDC_MCP_SEARCH_RESULT_MODE` | `references` (citations) or `content` (citations plus chunk body) | `references` |
| `ACDC_MCP_SEARCH_IN_MEMORY` | Hold the index in memory instead of on disk | `true` |
| `ACDC_MCP_SEARCH_KEYWORDS_BOOST` | Weight for frontmatter keyword matches | `3.0` |
| `ACDC_MCP_SEARCH_HEADING_BOOST` | Weight for heading-path matches | `2.5` |
| `ACDC_MCP_SEARCH_TITLE_BOOST` | Weight for document-title matches | `2.0` |
| `ACDC_MCP_SEARCH_PATH_BOOST` | Weight for path-label matches | `1.25` |
| `ACDC_MCP_SEARCH_CONTENT_BOOST` | Weight for body-content matches | `1.0` |

### Authentication (SSE only)

| Environment Variable | Description | Default |
|---|---|---|
| `ACDC_MCP_AUTH_TYPE` | `none`, `basic`, or `apikey` | `none` |
| `ACDC_MCP_AUTH_BASIC_USERNAME` | Basic auth username | — |
| `ACDC_MCP_AUTH_BASIC_PASSWORD` | Basic auth password | — |
| `ACDC_MCP_AUTH_API_KEYS` | Comma-separated API keys, sent as `X-API-Key` | — |

```bash
docker run -d -p 8080:8080 \
  -e ACDC_MCP_TRANSPORT=sse \
  -e ACDC_MCP_AUTH_TYPE=apikey \
  -e ACDC_MCP_AUTH_API_KEYS=key1,key2 \
  -v "$(pwd)/content:/app/content:ro" \
  sha1n/mcp-acdc-server:latest
```

Startup fails fast on contradictory auth configuration — for example `basic` without credentials, or `basic` combined with API keys.

Full reference: [Configuration Guide](https://github.com/sha1n/mcp-acdc-server/blob/master/docs/configuration.md).

---

## Health Check

In SSE mode the server exposes an **unauthenticated** `/health` endpoint returning `200 OK`, suitable for Kubernetes probes and Compose health checks:

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

---

## Docker Compose

```yaml
services:
  mcp-acdc-server:
    image: sha1n/mcp-acdc-server:latest
    ports:
      - "127.0.0.1:8080:8080"
    environment:
      - ACDC_MCP_CONTENT_DIR=/app/content
      - ACDC_MCP_TRANSPORT=sse
      - ACDC_MCP_HOST=0.0.0.0
      - ACDC_MCP_PORT=8080
    volumes:
      - ./content:/app/content:ro
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8080/health"]
      interval: 30s
      timeout: 3s
      start_period: 5s
      retries: 3
```

Ready-made deployment and content-configuration examples live in [`examples/`](https://github.com/sha1n/mcp-acdc-server/tree/master/examples):

- **[Local Content](https://github.com/sha1n/mcp-acdc-server/tree/master/examples/docker-local-content)** — direct bind mount, for rapid iteration.
- **[Image Content](https://github.com/sha1n/mcp-acdc-server/tree/master/examples/docker-image-content)** — production-like init-container pattern that ships content as its own image.
- **[Repository Docs](https://github.com/sha1n/mcp-acdc-server/tree/master/examples/repository-docs)** — chunk indexing over an existing repository's `docs/` directory.

---

## Connecting an Agent

**Claude Code:**

SSE:

```bash
claude mcp add --scope user --transport sse acdc http://<host>:8080/sse
```

stdio (the agent launches the container):

```bash
claude mcp add --scope user --transport stdio acdc -- docker run -i --rm -v "$(pwd)/content:/app/content:ro" sha1n/mcp-acdc-server:latest
```

**Gemini CLI:**

SSE:

```bash
gemini mcp add --scope user --transport sse --trust acdc http://<host>:8080/sse
```

stdio (the agent launches the container):

```bash
gemini mcp add --scope user --transport stdio --trust acdc docker -- run -i --rm -v "$(pwd)/content:/app/content:ro" sha1n/mcp-acdc-server:latest
```

For authenticated servers, supply the `Authorization` or `X-API-Key` header through your client's configuration.

---

## Features

- **Full-text search** — stemming, bounded fuzzy matching, and per-field boosting
- **Dynamic resource discovery** — content directories scanned automatically
- **Dynamic prompt discovery** — prompt templates scanned automatically
- **Content refresh** — `search`, `read`, and resource reads re-check the content root (debounced to once every 2s) and rebuild in place, so mid-session edits appear without a restart
- **MCP compliant** — works with any MCP-capable agent
- **Dual transport** — `stdio` for local agents, `sse` for remote/containerized deployments
- **Optional authentication** — basic auth or API keys

---

## License

Apache-2.0 — see [LICENSE](https://github.com/sha1n/mcp-acdc-server/blob/master/LICENSE).
