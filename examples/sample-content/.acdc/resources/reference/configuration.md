---
name: "Configuration Reference"
description: "CLI flags, environment variables, and authentication options"
keywords: [config, environment, flags, transport, authentication, uri-scheme]
---

# Configuration Reference

The ACDC server can be configured through CLI flags, environment variables, or a `.env` file.

## CLI Flags

| Flag | Env Variable | Default | Description |
|---|---|---|---|
| `--content-dir` | `ACDC_MCP_CONTENT_DIR` | `.` | Path to the content directory |
| `--transport` | `ACDC_MCP_TRANSPORT` | `stdio` | Transport protocol: `stdio` or `sse` |
| `--port` | `ACDC_MCP_PORT` | `8080` | Port for the SSE transport |
| `--uri-scheme` | `ACDC_MCP_URI_SCHEME` | `acdc` | Scheme used in resource URIs (e.g., `acdc://guides/getting-started`) |
| `--cross-ref` | `ACDC_MCP_CROSS_REF` | `false` | Transform relative Markdown links into `<scheme>://` URIs |
| `--auth-type` | `ACDC_MCP_AUTH_TYPE` | `none` | Authentication mode: `none`, `basic`, or `apikey` |

## Environment Variables

Any flag can be set via its corresponding environment variable. Variables are prefixed with `ACDC_MCP_` and use uppercase with underscores:

```bash
export ACDC_MCP_CONTENT_DIR=/path/to/content
export ACDC_MCP_TRANSPORT=sse
export ACDC_MCP_PORT=9090
```

You can also place these in a `.env` file in the working directory.

## Authentication

### None (default)

No authentication. Suitable for local development and stdio transport.

### Basic Auth

```bash
export ACDC_MCP_AUTH_TYPE=basic
export ACDC_MCP_AUTH_USERNAME=admin
export ACDC_MCP_AUTH_PASSWORD=secret
```

### API Key

```bash
export ACDC_MCP_AUTH_TYPE=apikey
export ACDC_MCP_AUTH_KEYS=key1,key2
```

Multiple keys can be provided as a comma-separated list.

## URI Scheme

By default, resources are exposed with the `acdc://` scheme. Use `--uri-scheme` to customise this — for example, `--uri-scheme myteam` produces URIs like `myteam://guides/getting-started`.

## See Also

- [Getting Started](../guides/getting-started.md) — Key concepts and first steps.
- [Search Features](search-features.md) — How the search engine works.
