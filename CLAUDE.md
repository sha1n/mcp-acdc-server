# ACDC MCP Server (mcp-acdc-server)

## Project Overview

The **Agent Content Discovery Companion (ACDC) MCP Server** is a high-performance Model Context Protocol (MCP) server designed to help AI agents discover and search local content. It bridges the gap between content repositories and AI agents, enabling team-specific knowledge management at scale.

- **Main Technologies:** Go 1.26, [Bleve](https://github.com/blevesearch/bleve) (Full-Text Search), [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk), [Cobra](https://github.com/spf13/cobra) (CLI), [Viper](https://github.com/spf13/viper) (Configuration).
- **Key Features:** Full-text search, dynamic resource and prompt discovery, dual transport (stdio/SSE), authentication (basic auth/API keys).
- **Architecture:** Follows a standard Go project layout:
  - `cmd/acdc-mcp/`: Entry point for the CLI application.
  - `internal/`: Core logic, including:
    - `app/`: Application runner and CLI coordination.
    - `mcp/`: MCP protocol implementation (tools, resources, prompts).
    - `search/`: Search service using Bleve.
    - `content/`, `resources/`, `prompts/`: Content management and providers.
    - `auth/`: Authentication middleware.
    - `config/`: Configuration management.
  - `docs/`: Comprehensive documentation on authoring, configuration, and development.
  - `examples/`: Deployment patterns (Docker, local content).

## Building and Running

The project uses a `Makefile` for common tasks.

### Key Commands

- **Build:**
  - `make build`: Builds binaries for all supported platforms (Linux, macOS, Windows).
  - `make build-current`: Builds for the current OS/Arch (output in `bin/`).
  - `make build-docker`: Builds the Docker image.
- **Run:**
  - `bin/acdc-mcp --content-dir ./content`: Runs the server with `stdio` transport (default).
  - `bin/acdc-mcp --transport sse --content-dir ./content`: Runs the server with `sse` transport.
- **Test:**
  - `make test`: Runs all Go tests.
  - `make coverage`: Runs tests and generates a coverage report.
- **Lint & Format:**
  - `make lint`: Runs `go vet`, `golangci-lint`, and format checks.
  - `make format`: Formats Go source files using `gofmt`.

### Development Setup

To add the local development server to your MCP clients:
- **Gemini CLI:** `make mcp-add-gemini-dev`
- **Claude Code:** `make mcp-add-claude-dev`

## Development Conventions

- **Code Style:** Strictly follow standard Go formatting (`gofmt`). This is enforced by CI and the `make lint` command.
- **Linters:** Use `make lint` to run all project linters. It includes `go vet` and `golangci-lint`.
- **Testing:** New features should include unit tests in the same directory (e.g., `*_test.go`) and, if applicable, integration tests in `tests/integration/`.
- **Dependencies:** Use `go mod tidy` to manage dependencies. `make install` is a shortcut for this.
- **Configuration:** Configuration is handled via flags, environment variables (prefixed with `ACDC_MCP_`), or configuration files via Viper.
- **Logging:** Use `log/slog` for structured logging. Logs are always directed to `stderr` to avoid interfering with the `stdio` transport.
