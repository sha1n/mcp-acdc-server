# Configuration Reference

The server can be configured using **CLI flags**, **environment variables**, or a **`.env` file**.

## Configuration Priority

When the same setting is specified in multiple places, the following priority applies (highest to lowest):

1. **CLI flags** — Explicit command-line arguments
2. **Environment variables** — Shell environment or exported vars
3. **`.env` file** — Key-value pairs in a `.env` file in the working directory
4. **Defaults** — Built-in fallback values

## General Settings

| CLI Flag | Short | Environment Variable | Description | Default |
|----------|-------|---------------------|-------------|---------|
| `--config` | `-c` | `ACDC_MCP_CONFIG` | Path to config file (required) | — |
| `--transport` | `-t` | `ACDC_MCP_TRANSPORT` | Transport type: `stdio` or `sse` | `stdio` |
| `--host` | `-H` | `ACDC_MCP_HOST` | Host for SSE server (SSE mode only) | `0.0.0.0` |
| `--port` | `-p` | `ACDC_MCP_PORT` | Port for SSE server (SSE mode only) | `8080` |
| `--search-max-results` | `-m` | `ACDC_MCP_SEARCH_MAX_RESULTS` | Maximum search results | `10` |
| `--search-keywords-boost` | — | `ACDC_MCP_SEARCH_KEYWORDS_BOOST` | Boost for keywords matches | `3.0` |
| `--search-name-boost` | — | `ACDC_MCP_SEARCH_NAME_BOOST` | Boost for name matches | `2.0` |
| `--search-content-boost` | — | `ACDC_MCP_SEARCH_CONTENT_BOOST` | Boost for content matches | `1.0` |

## Authentication Settings

| CLI Flag | Short | Environment Variable | Description | Default |
|----------|-------|---------------------|-------------|---------|
| `--auth-type` | `-a` | `ACDC_MCP_AUTH_TYPE` | Auth type: `none`, `basic`, or `apikey` | `none` |
| `--auth-basic-username` | `-u` | `ACDC_MCP_AUTH_BASIC_USERNAME` | Basic auth username | — |
| `--auth-basic-password` | `-P` | `ACDC_MCP_AUTH_BASIC_PASSWORD` | Basic auth password | — |
| `--auth-api-keys` | `-k` | `ACDC_MCP_AUTH_API_KEYS` | Comma-separated API keys | — |

## Config File Structure

The config file (typically `mcp-metadata.yaml`) defines server identity and content locations:

```yaml
server:
  name: "My MCP Server"
  version: "1.0.0"
  instructions: "Instructions for AI agents..."

content:
  - name: docs
    description: "Documentation and guides"
    path: ./documentation
  - name: internal
    description: "Internal resources"
    path: /absolute/path/to/internal

tools:  # Optional
  - name: search
    description: "Custom search description..."
  - name: read
    description: "Custom read description..."
```

### Content Location Fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Unique identifier for this content source (used in URIs and prompts) |
| `description` | Yes | Human-readable description (included in server instructions) |
| `path` | Yes | Path to content directory (relative to config file, or absolute) |

## Examples

**CLI flags (stdio mode - default):**
```bash
./bin/acdc-mcp -c /path/to/mcp-metadata.yaml
```

**CLI flags (SSE mode):**
```bash
./bin/acdc-mcp -t sse -c /path/to/mcp-metadata.yaml --port 9000
```

**CLI flags (SSE with basic auth):**
```bash
./bin/acdc-mcp -t sse -c /path/to/mcp-metadata.yaml --port 9000 --auth-type basic -u admin -P secret
```

**Environment variables:**
```bash
ACDC_MCP_TRANSPORT=sse ACDC_MCP_CONFIG=/path/to/mcp-metadata.yaml ./bin/acdc-mcp
```

**Using a `.env` file:**
```env
config=/path/to/mcp-metadata.yaml
transport=sse
port=9000
auth.type=basic
auth.basic.username=admin
auth.basic.password=secret
```

## Configuration Validation

The server validates configuration at startup and will fail with a clear error if:

- `--config` is not provided (required)
- Config file does not exist or is not readable
- Config file has no `content` section or no content locations
- Content location is missing required fields (name, description, path)
- Content location path does not exist
- Content location has no `mcp-resources/` directory
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
