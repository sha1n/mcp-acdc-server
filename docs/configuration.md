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
| `--content-dir` | `-c` | `ACDC_MCP_CONTENT_DIR` | Path to content directory | `./content` |
| `--transport` | `-t` | `ACDC_MCP_TRANSPORT` | Transport type: `stdio` or `sse` | `stdio` |
| `--host` | `-H` | `ACDC_MCP_HOST` | Host for SSE server (SSE mode only) | `0.0.0.0` |
| `--port` | `-p` | `ACDC_MCP_PORT` | Port for SSE server (SSE mode only) | `8080` |
| `--uri-scheme` | `-s` | `ACDC_MCP_URI_SCHEME` | URI scheme for resources (e.g. `acdc`, `myorg`) | `acdc` |
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

## Examples

**CLI flags (stdio mode - default):**
```bash
./bin/acdc-mcp -c /path/to/content
```

**CLI flags (SSE mode):**
```bash
./bin/acdc-mcp -t sse --port 9000
```

**CLI flags (SSE with basic auth):**
```bash
./bin/acdc-mcp -t sse --port 9000 --auth-type basic -u admin -P secret
```

**CLI flags (custom URI scheme):**
```bash
./bin/acdc-mcp -c /path/to/content --uri-scheme myorg
```

This produces resource URIs like `myorg://guides/getting-started` instead of the default `acdc://guides/getting-started`.

**Environment variables:**
```bash
ACDC_MCP_TRANSPORT=sse ACDC_MCP_CONTENT_DIR=/data ./bin/acdc-mcp
```

**Using a `.env` file:**
```env
transport=sse
port=9000
auth.type=basic
auth.basic.username=admin
auth.basic.password=secret
```

## Configuration Validation

The server validates configuration at startup and will fail with a clear error if:

- `--uri-scheme` is empty or doesn't match RFC 3986 (must start with a letter, then letters/digits/`+`/`-`/`.`)
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
