[![CI](https://github.com/sha1n/mcp-acdc-server-go/actions/workflows/ci.yml/badge.svg)](https://github.com/sha1n/mcp-acdc-server-go/actions/workflows/ci.yml)
[![CodeQL](https://github.com/sha1n/mcp-acdc-server-go/actions/workflows/codeql-analysis.yml/badge.svg)](https://github.com/sha1n/mcp-acdc-server-go/actions/workflows/codeql-analysis.yml)
[![codecov](https://codecov.io/gh/sha1n/mcp-acdc-server-go/graph/badge.svg?token=T67S1K956N)](https://codecov.io/gh/sha1n/mcp-acdc-server-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/sha1n/mcp-acdc-server-go)](https://goreportcard.com/report/github.com/sha1n/mcp-acdc-server-go)
[![Go Version](https://img.shields.io/github/go-mod/go-version/sha1n/mcp-acdc-server-go)](https://go.dev/)
[![License](https://img.shields.io/github/license/sha1n/mcp-acdc-server-go)](LICENSE)
[![Docker Image](https://img.shields.io/docker/v/sha1n/mcp-acdc-server-go?label=docker)](https://hub.docker.com/r/sha1n/mcp-acdc-server-go)

# mcp-acdc-server

**Agent Content Discovery Companion (ACDC) MCP Server** 🌈

ACDC is a high-performance Model Context Protocol (MCP) server designed to help AI agents discover and search through local content and resources dynamically. It provides a robust search interface and automatic resource discovery, making it easy for agents to find the context they need.

## 🚀 Features

- **Dynamic Resource Discovery**: Automatically scans and identifies resources from a configurable content directory.
- **Full-Text Search**: Provides a built-in search tool powered by [Bleve](https://github.com/blevesearch/bleve) for fast and efficient indexing/searching of local content.
- **MCP Compliant**: Fully supports the Model Context Protocol, enabling seamless integration with AI agents.
- **Dual Transport Support**: Works with both `stdio` (standard I/O) and `sse` (Server-Sent Events) transports.
- **Dockerized**: Simplified deployment with multi-stage Docker builds.
- **Cross-Platform**: Go-based implementation ensures compatibility across Linux, macOS, and Windows.

## 📋 Prerequisites

- [Go](https://go.dev/doc/install) 1.24 or later (for local builds)
- [Docker](https://docs.docker.com/get-docker/) (optional, for containerized execution)
- [Make](https://www.gnu.org/software/make/) (recommended for easy orchestration)

## 🛠️ Installation & Setup

### Building From Source

```bash
# Clone the repository
git clone https://github.com/sha1n/mcp-acdc-server-go.git
cd mcp-acdc-server-go

# Install dependencies
make install

# Build the binary
make build-current # Builds for your current OS/Arch
```

### Building Docker Image

```bash
make build-docker
```

## 🏃 Running the Server

### Local Execution

By default, the server starts with SSE transport on port 8000:

```bash
./bin/mcp-acdc
```

### Using Stdio (Common for Local Agent Integration)

```bash
ACDC_MCP_TRANSPORT=stdio ./bin/mcp-acdc
```

### Docker Execution

```bash
docker run -p 8000:8000 \
  -v $(pwd)/content:/app/content \
  sha1n/mcp-acdc:latest
```

## ⚙️ Configuration

The server can be configured using environment variables or a `.env` file in the working directory.

| Variable                       | Description                                        | Default     |
| ------------------------------ | -------------------------------------------------- | ----------- |
| `ACDC_MCP_CONTENT_DIR`         | Path to the directory containing content/resources | `./content` |
| `ACDC_MCP_TRANSPORT`           | Server transport type (`stdio` or `sse`)           | `sse`       |
| `ACDC_MCP_HOST`                | Host for SSE server                                | `0.0.0.0`   |
| `ACDC_MCP_PORT`                | Port for SSE server                                | `8000`      |
| `ACDC_MCP_SEARCH_MAX_RESULTS`  | Maximum number of search results to return         | `10`        |
| `ACDC_MCP_SEARCH_HEAP_SIZE_MB` | Heap size limit for the search indexer             | `50`        |

### Authentication

The server supports optional authentication to secure access. Configure using the following environment variables:

| Variable                       | Description                                       | Default                        |
| ------------------------------ | ------------------------------------------------- | ------------------------------ |
| `ACDC_MCP_AUTH_TYPE`           | Authentication type: `none`, `basic`, or `apikey` | `none`                         |
| `ACDC_MCP_AUTH_BASIC_USERNAME` | Username for Basic auth                           | (required if type is `basic`)  |
| `ACDC_MCP_AUTH_BASIC_PASSWORD` | Password for Basic auth                           | (required if type is `basic`)  |
| `ACDC_MCP_AUTH_API_KEYS`       | Comma-separated list of valid API keys            | (required if type is `apikey`) |

**Example - Basic Auth:**
```bash
ACDC_MCP_AUTH_TYPE=basic \
ACDC_MCP_AUTH_BASIC_USERNAME=admin \
ACDC_MCP_AUTH_BASIC_PASSWORD=secret \
./bin/mcp-acdc
```

**Example - API Key Auth:**
```bash
ACDC_MCP_AUTH_TYPE=apikey \
ACDC_MCP_AUTH_API_KEYS="key1,key2,key3" \
./bin/mcp-acdc
```

API keys must be provided via the `X-API-Key` header in HTTP requests.

> [!CAUTION]
> **Security Best Practices:**
> - Never commit credentials to version control. Ensure `.env` files are in `.gitignore`.
> - Use a secrets manager (e.g., HashiCorp Vault, AWS Secrets Manager) in production.
> - For containerized deployments, use Kubernetes Secrets or Docker secrets.
> - Rotate credentials regularly and use strong, unique passwords/keys.

### Content Metadata
The server expects an `mcp-metadata.yaml` file in the root of your content directory to define server identity.

## 🛠️ Development

Use the provided `Makefile` for common tasks:

- `make install`: Tidy Go modules.
- `make build`: Build binaries for all supported platforms.
- `make test`: Run all tests.
- `make lint`: Run linters.
- `make format`: Format source files.
- `make clean`: Remove build artifacts.

## 📄 License

This project is licensed under the MIT License - see the `LICENSE` file for details.
