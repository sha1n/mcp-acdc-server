# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

ACDC (Agent Content Discovery Companion) is an MCP server that provides AI agents with full-text search and resource discovery capabilities over local Markdown content. It uses the official MCP Go SDK and Bleve for search indexing. Supports multiple named content locations with source filtering.

## Build & Development Commands

```bash
# Build for current platform
make go-build-current

# Run all tests
make test

# Run a single test
go test -run TestFunctionName ./path/to/package

# Run tests with coverage
make coverage

# Lint and format
make lint
make format

# Build Docker image
make build-docker

# Add dev server to Claude Code (uses examples/sample-content)
make mcp-add-claude-dev
```

## Architecture

### Package Structure

- `cmd/acdc-mcp/` - CLI entrypoint using Cobra
- `internal/app/` - Application wiring: CLI flags, server factory, SSE/stdio runners
- `internal/mcp/` - MCP server creation and tool registration (search, read)
- `internal/adapters/` - **Adapter system for flexible content structures**
- `internal/content/` - Content provider for multiple locations (structure-agnostic)
- `internal/resources/` - Resource discovery and provider
- `internal/prompts/` - Prompt discovery and provider
- `internal/search/` - Bleve-based search service with source filtering
- `internal/config/` - Settings loading (viper-based, supports env vars and flags)
- `internal/auth/` - Authentication middleware (basic, apikey)
- `internal/domain/` - Core types (metadata, content locations, search results)

### Key Flow

1. `cmd/acdc-mcp/main.go` → `app.RunWithDeps()` handles CLI and starts server
2. `app.CreateMCPServer()` initializes all components:
   - Loads config file (`--config` flag) for server identity and content locations
   - Creates adapter registry with ACDC and Legacy adapters
   - For each content location: resolves adapter (explicit or auto-detect)
   - Discovers resources and prompts using adapter-based discovery
   - Indexes resources into Bleve search (with source tagging)
   - Creates MCP server with tools and resources
3. Server runs in either `stdio` mode (default) or `sse` mode (HTTP)

### Config File Structure

The server requires a config file (typically `mcp-metadata.yaml`):
```yaml
server:
  name: "Server Name"
  version: "1.0.0"
  instructions: "Instructions for agents..."

content:
  - name: docs           # Source identifier (used in URIs)
    description: "Docs"  # Human-readable description
    path: ./docs         # Relative to config file, or absolute
    type: acdc-mcp       # Optional: adapter type (acdc-mcp, legacy). Omit for auto-detect.
```

### Content Location Structure

ACDC supports two directory structures through an **adapter system**:

**ACDC Native (Preferred):**
```
location-path/
├── resources/           # Markdown resources with YAML frontmatter
└── prompts/            # Prompt templates (optional)
```

**Legacy (Backward Compatibility):**
```
location-path/
├── mcp-resources/       # Markdown resources with YAML frontmatter
└── mcp-prompts/         # Prompt templates (optional)
```

The server **auto-detects** the structure (checks `resources/` first, falls back to `mcp-resources/`).
You can explicitly specify the adapter with `type: acdc-mcp` or `type: legacy` in the config.

### Adapter System

The adapter pattern enables flexible content structure support:

- **`acdc-mcp` adapter**: Native structure (`resources/`, `prompts/`)
- **`legacy` adapter**: Backward compatibility (`mcp-resources/`, `mcp-prompts/`)
- **Auto-detection**: Inspects directory structure if no type specified
- **Extensible**: Foundation for future adapters (e.g., Claude Code plugins)

See `internal/adapters/` for adapter implementations.

### URI Scheme and Namespacing

- Resources: `acdc://<source>/<path>` (e.g., `acdc://docs/guides/getting-started`)
- Prompts: `<source>:<name>` (e.g., `docs:code-review`)

### Integration Tests

Integration tests in `tests/integration/` use a testkit that:
- Creates temporary content directories and config files
- Spawns real MCP servers
- Uses the typed MCP SDK client for assertions

See `tests/integration/testkit/` for helpers like `CreateTestContentDir()` and `NewTestFlags()`.
