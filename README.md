<div align="center">

[![CI](https://github.com/sha1n/mcp-acdc-server/actions/workflows/ci.yml/badge.svg)](https://github.com/sha1n/mcp-acdc-server/actions/workflows/ci.yml)
[![CodeQL](https://github.com/sha1n/mcp-acdc-server/actions/workflows/codeql-analysis.yml/badge.svg)](https://github.com/sha1n/mcp-acdc-server/actions/workflows/codeql-analysis.yml)
[![codecov](https://codecov.io/gh/sha1n/mcp-acdc-server/graph/badge.svg?token=T67S1K956N)](https://codecov.io/gh/sha1n/mcp-acdc-server)
[![Go Report Card](https://goreportcard.com/badge/github.com/sha1n/mcp-acdc-server)](https://goreportcard.com/report/github.com/sha1n/mcp-acdc-server)
[![Go Version](https://img.shields.io/github/go-mod/go-version/sha1n/mcp-acdc-server)](https://go.dev/)
[![License](https://img.shields.io/github/license/sha1n/mcp-acdc-server)](LICENSE)
[![Docker Image](https://img.shields.io/docker/v/sha1n/mcp-acdc-server?label=docker)](https://hub.docker.com/r/sha1n/mcp-acdc-server)

</div>

# mcp-acdc-server

**Agent Content Discovery Companion (ACDC) MCP Server**

A high-performance Model Context Protocol (MCP) server for AI agents to discover and search local content. Features full-text search powered by [Bleve](https://github.com/blevesearch/bleve), dual transport support (stdio/SSE), and flexible authentication.

## 🌐 Why ACDC?

ACDC solves the challenge of managing team-specific knowledge in the AI agent space. While general-purpose LLMs are powerful, they lack the context of your team's specific tools, patterns, and standards.

ACDC provides a bridge between your content repositories and AI agents, making it easy to manage and develop relevant context at scale.

### Deployment Patterns

*   **Centralized (SSE):** For large teams, deploy ACDC as a central service. Agents across the organization can connect via the SSE interface, making knowledge management transparent and consistent.
*   **Localized (stdio):** For smaller teams or individual developers, running ACDC locally might be good enough. Point it at a Git-managed content repository and pull changes as needed.

**Docker (Quick Start):**
```bash
cd examples/docker-local-content
docker-compose up -d
```

**Homebrew:**
```bash
brew install sha1n/tap/acdc-mcp
acdc-mcp --config ./mcp-metadata.yaml
```

## ✨ Features

- **Full-Text Search** — Fast indexing with stemming, fuzzy matching, and configurable boosting
- **Multiple Content Locations** — Organize content from different sources with named locations
- **Source Filtering** — Search within specific content sources
- **Dynamic Resource Discovery** — Automatic scanning of content directories
- **Dynamic Prompt Discovery** — Automatic scanning of prompt templates with namespacing
- **MCP Compliant** — Seamless integration with AI agents
- **Dual Transport** — `stdio` for local agents, `sse` for remote/Docker
- **Authentication** — Optional basic auth or API key protection
- **Cross-Platform** — Linux, macOS, and Windows

## 📦 Installation

### Docker
```bash
docker pull sha1n/mcp-acdc-server:latest
```

### Homebrew
```bash
brew install sha1n/tap/acdc-mcp
```

### Build from Source
See [Development Guide](docs/development.md) for build instructions.

## 🏃 Running

### Stdio Transport (default)
```bash
acdc-mcp --config ./mcp-metadata.yaml
```

### SSE Transport
```bash
acdc-mcp --transport sse --config ./mcp-metadata.yaml
```

### Docker
```bash
docker run -p 8080:8080 \
  -v $(pwd)/content:/app/content \
  -e ACDC_MCP_CONFIG=/app/content/mcp-metadata.yaml \
  sha1n/mcp-acdc-server:latest
```

### Health Check (SSE Only)
The SSE server exposes an unauthenticated `/health` endpoint that returns `200 OK`. This can be used as a liveness or readiness probe in Kubernetes:

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


## ⚙️ Configuration

| Flag | Short | Environment Variable | Default |
|------|-------|---------------------|---------|
| `--config` | `-c` | `ACDC_MCP_CONFIG` | (required) |
| `--transport` | `-t` | `ACDC_MCP_TRANSPORT` | `stdio` |
| `--port` | `-p` | `ACDC_MCP_PORT` | `8080` |
| `--search-max-results` | `-m` | `ACDC_MCP_SEARCH_MAX_RESULTS` | `10` |
| `--search-keywords-boost` | — | `ACDC_MCP_SEARCH_KEYWORDS_BOOST` | `3.0` |
| `--auth-type` | `-a` | `ACDC_MCP_AUTH_TYPE` | `none` |

For full configuration options including authentication, see [Configuration Reference](docs/configuration.md).

## 🤖 Agent Configuration

### [Gemini CLI](https://github.com/google-gemini/gemini-cli)

**Stdio:**
```bash
gemini mcp add --scope user --transport stdio --trust acdc acdc-mcp -- --transport stdio --config $ACDC_MCP_CONFIG
```

**SSE:**
```bash
gemini mcp add --scope user --transport sse --trust acdc http://<host>:<port>/sse
```

### [Claude Code](https://docs.anthropic.com/en/docs/agents-and-tools/claude-code)

**Stdio:**
```bash
claude mcp add --scope user --transport stdio acdc -- acdc-mcp --transport stdio --config $ACDC_MCP_CONFIG
```

**SSE:**
```bash
claude mcp add --scope user --transport sse acdc http://<host>:<port>/sse
```

> [!NOTE]
> For authenticated servers, provide the required headers (`Authorization` or `X-API-Key`) as part of the client configuration.

## 📚 Content & Resources

### Config File Structure

The server requires a config file (typically `mcp-metadata.yaml`) that defines server identity and content locations:

```yaml
server:
  name: "My MCP Server"
  version: "1.0.0"
  instructions: "Server instructions for AI agents..."

content:
  - name: docs
    description: "Public documentation and guides"
    path: ./documentation
  - name: internal
    description: "Internal runbooks and procedures"
    path: ./internal

tools:  # Optional - server provides default descriptions
  - name: search
    description: "Custom search description..."
  - name: read
    description: "Custom read description..."
```

### Content Location Structure

Each content location should have the following structure:

```
location-path/
├── mcp-resources/       # Markdown resources with YAML frontmatter
│   └── guides/
│       └── getting-started.md
└── mcp-prompts/         # Optional: prompt templates
    └── code-review.md
```

### URI Scheme

Resources are addressed using URIs that include the source name:
- `acdc://<source>/<path>` (e.g., `acdc://docs/guides/getting-started`)

### Prompt Namespacing

Prompts are namespaced by their source:
- `<source>:<prompt-name>` (e.g., `docs:code-review`)

### Search Source Filtering

The search tool supports an optional `source` parameter to filter results to a specific content source.

For details on authoring resource files, including frontmatter format and search keyword boosting, see the [Authoring Resources Guide](docs/authoring-resources.md).

### Examples

Check out the [examples/](examples/) directory for structured deployment patterns:
- [Local Content Demo](examples/docker-local-content/) — Direct mount for rapid iteration.
- [Remote Image Demo](examples/docker-image-content/) — Production-like init container pattern.

## 🛠️ Development

See [Development Guide](docs/development.md) for building, testing, and contributing.

## 📄 License

MIT License - see [LICENSE](LICENSE) for details.
