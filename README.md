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

By default, the server starts with SSE transport on port 8080:

```bash
./bin/mcp-acdc
```

### Using Stdio (Common for Local Agent Integration)

```bash
ACDC_MCP_TRANSPORT=stdio ./bin/mcp-acdc
```

### Docker Execution

```bash
docker run -p 8080:8080 \
  -v $(pwd)/content:/app/content \
  sha1n/mcp-acdc:latest
```

## ⚙️ Configuration

The server can be configured using **CLI flags**, **environment variables**, or a **`.env` file**. 

### Configuration Priority

When the same setting is specified in multiple places, the following priority applies (highest to lowest):

1. **CLI flags** — Explicit command-line arguments
2. **Environment variables** — Shell environment or exported vars
3. **`.env` file** — Key-value pairs in a `.env` file in the working directory
4. **Defaults** — Built-in fallback values

### General Settings

| CLI Flag | Short | Environment Variable | Description | Default |
|----------|-------|---------------------|-------------|---------|
| `--content-dir` | `-c` | `ACDC_MCP_CONTENT_DIR` | Path to content directory | `./content` |
| `--transport` | `-t` | `ACDC_MCP_TRANSPORT` | Transport type: `stdio` or `sse` | `sse` |
| `--host` | `-H` | `ACDC_MCP_HOST` | Host for SSE server | `0.0.0.0` |
| `--port` | `-p` | `ACDC_MCP_PORT` | Port for SSE server | `8080` |
| `--search-max-results` | `-m` | `ACDC_MCP_SEARCH_MAX_RESULTS` | Maximum search results | `10` |

### Authentication Settings

| CLI Flag | Short | Environment Variable | Description | Default |
|----------|-------|---------------------|-------------|---------|
| `--auth-type` | `-a` | `ACDC_MCP_AUTH_TYPE` | Auth type: `none`, `basic`, or `apikey` | `none` |
| `--auth-basic-username` | `-u` | `ACDC_MCP_AUTH_BASIC_USERNAME` | Basic auth username | — |
| `--auth-basic-password` | `-P` | `ACDC_MCP_AUTH_BASIC_PASSWORD` | Basic auth password | — |
| `--auth-api-keys` | `-k` | `ACDC_MCP_AUTH_API_KEYS` | Comma-separated API keys | — |

### Examples

**Using CLI flags (stdio mode):**
```bash
./bin/mcp-acdc -t stdio -c /path/to/content
```

**Using CLI flags (SSE with basic auth):**
```bash
./bin/mcp-acdc --port 9000 --auth-type basic -u admin -P secret
```

**Using environment variables:**
```bash
ACDC_MCP_TRANSPORT=stdio ACDC_MCP_CONTENT_DIR=/data ./bin/mcp-acdc
```

**Using a `.env` file:**
```env
transport=sse
port=9000
auth.type=basic
auth.basic.username=admin
auth.basic.password=secret
```

### Configuration Validation

The server validates configuration at startup and will fail with a clear error if:
- `--auth-type=basic` is set without username/password
- `--auth-type=apikey` is set without API keys
- `--auth-type=none` is set with auth credentials (conflicting intent)
- `--auth-type=basic` is combined with `--auth-api-keys` (mutually exclusive)

API keys must be provided via the `X-API-Key` header in HTTP requests.

> [!CAUTION]
> **Security Best Practices:**
> - Never commit credentials to version control. Ensure `.env` files are in `.gitignore`.
> - Use a secrets manager (e.g., HashiCorp Vault, AWS Secrets Manager) in production.
> - For containerized deployments, use Kubernetes Secrets or Docker secrets.
> - Rotate credentials regularly and use strong, unique passwords/keys.

### Content Metadata
The server expects an `mcp-metadata.yaml` file in the root of your content directory to define server identity.

For details on authoring resource files, including frontmatter format and search keyword boosting, see the [Authoring Resources Guide](docs/authoring-resources.md).

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
