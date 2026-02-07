# ACDC MCP Server Specifications

## Functional Specifications

The ACDC (Agent Content Discovery Companion) MCP Server is designed to serve organization-wide knowledge base resources to AI agents via the Model Context Protocol (MCP). It acts as a bridge between static content repositories and AI agents, providing discovery, search, and retrieval capabilities.

### Core Principles
1.  **Centralized Content**: Operates on a local directory (typically a mounted volume) containing static Markdown resources.
2.  **Zero-Config Client**: Clients discover capabilities dynamically via MCP tool definitions.
3.  **Metadata-Driven**: Server identity and tool exposure are controlled by a `mcp-metadata.yaml` manifest in the content root.
4.  **Transport Agnostic**: Supports both `stdio` (local process) and `sse` (HTTP) transports.

---

## Configuration

The server is configured via environment variables, command-line flags, or a `.env` file in the current working directory. CLI flags take precedence over environment variables, which take precedence over `.env` file values.

| Environment Variable | CLI Flag | Description | Default |
| :--- | :--- | :--- | :--- |
| `ACDC_MCP_CONTENT_DIR` | `--content-dir`, `-c` | Root directory containing `mcp-metadata.yaml` and `mcp-resources/`. | `./content` |
| `ACDC_MCP_TRANSPORT` | `--transport`, `-t` | Communication transport: `stdio` or `sse`. | `stdio` |
| `ACDC_MCP_HOST` | `--host`, `-H` | Host interface to bind for SSE transport. | `0.0.0.0` |
| `ACDC_MCP_PORT` | `--port`, `-p` | Port to listen on for SSE transport. | `8080` |
| `ACDC_MCP_SEARCH_MAX_RESULTS` | `--search-max-results`, `-m` | Max results returned by the search tool. | `10` |
| `ACDC_MCP_SEARCH_KEYWORDS_BOOST` | `--search-keywords-boost` | Boost factor for keyword matches. | `3.0` |
| `ACDC_MCP_SEARCH_NAME_BOOST` | `--search-name-boost` | Boost factor for name matches. | `2.0` |
| `ACDC_MCP_SEARCH_CONTENT_BOOST` | `--search-content-boost` | Boost factor for content matches. | `1.0` |
| `ACDC_MCP_AUTH_TYPE` | `--auth-type`, `-a` | Authentication mode for SSE: `none`, `basic`, `apikey`. | `none` |
| `ACDC_MCP_AUTH_BASIC_USERNAME` | `--auth-basic-username`, `-u` | Username for Basic Auth. | - |
| `ACDC_MCP_AUTH_BASIC_PASSWORD` | `--auth-basic-password`, `-P` | Password for Basic Auth. | - |
| `ACDC_MCP_URI_SCHEME` | `--uri-scheme`, `-s` | URI scheme for resource URIs (RFC 3986 compliant). | `acdc` |
| `ACDC_MCP_AUTH_API_KEYS` | `--auth-api-keys`, `-k` | Comma-separated list of valid API keys for `apikey` auth. | - |

---

## Content Repository Structure

The server expects a specific directory structure within `ACDC_MCP_CONTENT_DIR`:

```text
/ (Content Root)
├── mcp-metadata.yaml       # Server identity and tool configuration (Required)
└── mcp-resources/          # Directory containing resource files (Required)
    ├── guide.md
    └── subfolder/
        └── details.md
```

### 1. Metadata Manifest (`mcp-metadata.yaml`)

Defines the server's identity and optional tool overrides.

**Schema:**
```yaml
server:
  name: <string>        # Display name of the MCP server
  version: <string>     # Semantic version string
  instructions: <string> # System prompt / context instructions for the agent

tools:                  # Optional: Override default tool descriptions
  - name: search
    description: <string> 
  - name: read
    description: <string> 
```
*Note: If the `tools` section is omitted or a specific tool is not listed, the server provides high-quality default descriptions for the `search` and `read` tools.*

### 2. Resources (`mcp-resources/`)

-   **Discovery**: The server recursively scans `mcp-resources/` for `.md` files.
-   **URI Scheme**: `<scheme>://<relative_path_without_extension>` (default scheme: `acdc`)
    -   Example: `mcp-resources/docs/guide.md` -> `acdc://docs/guide`
    -   With `--uri-scheme myorg`: `mcp-resources/docs/guide.md` -> `myorg://docs/guide`
    -   The scheme must be RFC 3986 compliant (starts with a letter, followed by letters/digits/`+`/`-`/`.`).
    -   Windows backslashes are normalized to forward slashes.
-   **File Format**: Must be Markdown with YAML Frontmatter.

**Frontmatter Requirements:**
```markdown
---
name: <string>          # Required: Human-readable title
description: <string>   # Required: Brief summary for listing
keywords:               # Optional: List of keywords for search boosting
  - tag1
  - tag2
---
Markdown content follows...
```

---

## Tools

The server always implements and registers the following MCP tools. Their descriptions can be customized via `mcp-metadata.yaml`, but sensible defaults are provided.

### `search`
Performs a full-text search across all indexed resources.

*   **Input Schema:**
    ```json
    {
      "query": "string (Required) - Natural language or keyword query"
    }
    ```
*   **Behavior:**
    *   Searches against `name`, `content`, and `keywords` using fuzzy matching (distance 1) and stemming.
    *   Applies boosting: `keywords` (3.0), `name` (2.0), `content` (1.0) by default.
    *   Returns a maximum of `ACDC_MCP_SEARCH_MAX_RESULTS`.
*   **Output:**
    Text summary of results in the format:
    ```text
    Search results for '<query>':

    - [<Name>](<URI>): <Snippet> (relevance: <Score>)
    ...
    ```
    *If no results found, returns a descriptive message.*

### `read`
Retrieves the full raw content of a resource.

*   **Input Schema:**
    ```json
    {
      "uri": "string (Required) - The resource URI (e.g. acdc://path)"
    }
    ```
*   **Behavior:**
    *   Resolves the URI to the corresponding file path.
    *   Reads the file content (excluding frontmatter, effectively returning the body).
*   **Output:**
    Raw string content of the markdown body.

---

## MCP Resources

In addition to tools, the server exposes resources directly via the MCP `resources/list` capability.

*   **URI**: Same as the `<scheme>://` URI used in tools (default scheme: `acdc`).
*   **Name**: From frontmatter `name`.
*   **Description**: From frontmatter `description`.
*   **MIME Type**: `text/markdown`.

---

## Transports

The server supports two transport modes, which are **mutually exclusive**. Only one transport can be active at a time.

### Stdio (Default)
*   **Standard Input**: Receives JSON-RPC messages.
*   **Standard Output**: Sends JSON-RPC responses.
*   **Standard Error**: Structured logs (JSON or text).

### SSE (Server-Sent Events)
Used for remote connections.

*   **GET /sse**: Establishes the event stream.
*   **POST /messages**: Endpoint for client JSON-RPC requests.
*   **GET /health**: Health check (200 OK). Always public.

**Authentication (SSE Only):**
*   **Basic**: Standard `Authorization: Basic <base64>` header.
*   **API Key**: `X-API-Key: <key>` header.
*   *Note: Only `/health` is always public.*

---

## Search Implementation Details

*   **Engine**: Bleve (Go) full-text search engine.
*   **Indexing**: Occurs at server startup (in-memory or temporary directory).
*   **Features**:
    *   **Fuzzy Search**: Matches terms with an edit distance of 1.
    *   **Stemming**: Uses the standard English analyzer for language-aware matching.
    *   **Highlighting**: Generates dynamic snippets with search term context.
*   **Indexed Fields (Default Boosts)**:
    *   `uri` (Stored, Indexed)
    *   `name` (Stored, Indexed, Boost x2.0)
    *   `content` (Stored, Indexed, Boost x1.0)
    *   `keywords` (Indexed, Boost x3.0, Optional)