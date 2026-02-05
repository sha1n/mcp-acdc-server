---
name: "Configuration Guide"
description: "Detailed configuration options for MCP ACDC"
---

# Configuration Guide

The MCP ACDC server can be configured using CLI flags, environment variables, or a `.env` file.

## Config File

The server requires a config file (typically `mcp-metadata.yaml`) that defines server identity and content locations:

```yaml
server:
  name: "My Server"
  version: "1.0.0"
  instructions: "Server instructions..."

content:
  - name: docs
    description: "Documentation"
    path: ./docs
```

## Environment Variables

- `ACDC_MCP_CONFIG`: Path to the config file (required).
- `ACDC_MCP_TRANSPORT`: `stdio` or `sse`.
- `ACDC_MCP_PORT`: The port for the SSE server (default: 8080).
- `ACDC_MCP_AUTH_TYPE`: `none`, `basic`, or `apikey`.

## Authentication

Go version of ACDC supports:
- **Basic Auth**: Using username and password.
- **API Key**: Using one or more authorized keys.
