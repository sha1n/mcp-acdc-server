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

## 🚀 Quick Start

**Docker (recommended):**
```bash
docker run -p 8080:8080 -v $(pwd)/content:/app/content sha1n/mcp-acdc-server:latest
```

**Homebrew:**
```bash
brew install sha1n/tap/acdc-mcp
acdc-mcp --content-dir ./content
```

## ✨ Features

- **Full-Text Search** — Fast indexing and search with keyword boosting
- **Dynamic Resource Discovery** — Automatic scanning of content directories
- **MCP Compliant** — Seamless integration with AI agents
- **Dual Transport** — `stdio` for local agents, `sse` for remote/Docker
- **Authentication** — Optional basic auth or API key protection
- **Cross-Platform** — Linux, macOS, and Windows

## � Installation

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

### SSE Transport (default)
```bash
acdc-mcp --content-dir ./content
```

### Stdio Transport
```bash
acdc-mcp --transport stdio --content-dir ./content
```

### Docker
```bash
docker run -p 8080:8080 \
  -v $(pwd)/content:/app/content \
  sha1n/mcp-acdc-server:latest
```

## ⚙️ Configuration

| Flag | Short | Environment Variable | Default |
|------|-------|---------------------|---------|
| `--content-dir` | `-c` | `ACDC_MCP_CONTENT_DIR` | `./content` |
| `--transport` | `-t` | `ACDC_MCP_TRANSPORT` | `sse` |
| `--port` | `-p` | `ACDC_MCP_PORT` | `8080` |
| `--auth-type` | `-a` | `ACDC_MCP_AUTH_TYPE` | `none` |

For full configuration options including authentication, see [Configuration Reference](docs/configuration.md).

## 🤖 Agent Configuration

### [Gemini CLI](https://github.com/google-gemini/gemini-cli)

**Stdio:**
```bash
gemini mcp add --scope user --transport stdio --trust acdc acdc-mcp -- --transport stdio --content-dir $ACDC_MCP_CONTENT_DIR
```

**SSE:**
```bash
gemini mcp add --scope user --transport sse --trust acdc http://<host>:<port>/sse
```

### [Claude Code](https://docs.anthropic.com/en/docs/agents-and-tools/claude-code)

**Stdio:**
```bash
claude mcp add --scope user --transport stdio acdc -- acdc-mcp --transport stdio --content-dir $ACDC_MCP_CONTENT_DIR
```

**SSE:**
```bash
claude mcp add --scope user --transport sse acdc http://<host>:<port>/sse
```

> [!NOTE]
> For authenticated servers, provide the required headers (`Authorization` or `X-API-Key`) as part of the client configuration.

## 📚 Content & Resources

The server requires an `mcp-metadata.yaml` file in your content directory to define server identity.

For details on authoring resource files, including frontmatter format and search keyword boosting, see the [Authoring Resources Guide](docs/authoring-resources.md).

## 🛠️ Development

See [Development Guide](docs/development.md) for building, testing, and contributing.

## 📄 License

MIT License - see [LICENSE](LICENSE) for details.
