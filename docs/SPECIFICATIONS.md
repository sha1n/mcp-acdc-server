# ACDC MCP Server Specifications

## Functional Specifications

The ACDC (Agent Content Discovery Companion) MCP Server is designed to serve organization-wide knowledge base resources to AI agents via the Model Context Protocol (MCP). It acts as a bridge between static content repositories and AI agents, providing discovery, search, and retrieval capabilities.

### Core Principles
1.  **Multiple Content Sources**: Supports multiple named content locations, each with its own resources and prompts.
2.  **Zero-Config Client**: Clients discover capabilities dynamically via MCP tool definitions.
3.  **Metadata-Driven**: Server identity and content locations are controlled by a config file (typically `mcp-metadata.yaml`).
4.  **Transport Agnostic**: Supports both `stdio` (local process) and `sse` (HTTP) transports.

---

## Configuration

The server is configured via environment variables, command-line flags, or a `.env` file in the current working directory. CLI flags take precedence over environment variables, which take precedence over `.env` file values.

| Environment Variable | CLI Flag | Description | Default |
| :--- | :--- | :--- | :--- |
| `ACDC_MCP_CONFIG` | `--config`, `-c` | Path to the config file containing server metadata and content locations. | (required) |
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
| `ACDC_MCP_AUTH_API_KEYS` | `--auth-api-keys`, `-k` | Comma-separated list of valid API keys for `apikey` auth. | - |

---

## Config File Structure

The config file (typically `mcp-metadata.yaml`) defines the server's identity and content locations:

```yaml
server:
  name: <string>        # Display name of the MCP server
  version: <string>     # Semantic version string
  instructions: <string> # System prompt / context instructions for the agent

content:                # Required: List of content locations
  - name: <string>      # Unique identifier for this content source
    description: <string> # Human-readable description
    path: <string>      # Path to content directory (relative to config file or absolute)

tools:                  # Optional: Override default tool descriptions
  - name: search
    description: <string>
  - name: read
    description: <string>
```

*Note: If the `tools` section is omitted or a specific tool is not listed, the server provides high-quality default descriptions for the `search` and `read` tools.*

### Content Location Structure

Each content location should have the following structure:

```text
<location-path>/
├── mcp-resources/          # Directory containing resource files (Required)
│   ├── guide.md
│   └── subfolder/
│       └── details.md
└── mcp-prompts/            # Optional: Directory containing prompt templates
    └── code-review.md
```

---

## Resources

### Discovery
-   The server recursively scans each content location's `mcp-resources/` directory for `.md` files.

### URI Scheme
Resources are addressed using URIs that include the source name:
-   Format: `acdc://<source>/<relative_path_without_extension>`
-   Example: For a resource at `docs/mcp-resources/guides/getting-started.md` in source "docs":
    -   URI: `acdc://docs/guides/getting-started`
-   Windows backslashes are normalized to forward slashes.

### File Format
Must be Markdown with YAML Frontmatter:

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

## Prompts

### Discovery
-   The server scans each content location's `mcp-prompts/` directory (if it exists) for `.md` files.

### Namespacing
Prompts are namespaced by their source:
-   Format: `<source>:<prompt-name>`
-   Example: For a prompt at `docs/mcp-prompts/code-review.md` in source "docs":
    -   Name: `docs:code-review`

### File Format
Prompt files use YAML frontmatter to define metadata and arguments:

```markdown
---
name: <string>          # Prompt name (without namespace)
description: <string>   # Brief description
arguments:
  - name: <string>      # Argument name
    description: <string>
    required: <boolean>
---
Template content with {{.argumentName}} placeholders...
```

---

## Tools

The server always implements and registers the following MCP tools. Their descriptions can be customized via the config file, but sensible defaults are provided.

### `search`
Performs a full-text search across all indexed resources.

*   **Input Schema:**
    ```json
    {
      "query": "string (Required) - Natural language or keyword query",
      "source": "string (Optional) - Filter results to a specific content source"
    }
    ```
*   **Behavior:**
    *   Searches against `name`, `content`, and `keywords` using fuzzy matching (distance 1) and stemming.
    *   Applies boosting: `keywords` (3.0), `name` (2.0), `content` (1.0) by default.
    *   If `source` is provided, filters results to only that content source.
    *   Returns a maximum of `ACDC_MCP_SEARCH_MAX_RESULTS`.
*   **Output:**
    Text summary of results in the format:
    ```text
    Search results for '<query>':

    - [<Source>] [<Name>](<URI>): <Snippet> (relevance: <Score>)
    ...
    ```
    *If no results found, returns a descriptive message.*

### `read`
Retrieves the full raw content of a resource.

*   **Input Schema:**
    ```json
    {
      "uri": "string (Required) - The acdc:// URI of the resource (e.g., acdc://docs/guide)"
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

*   **URI**: `acdc://<source>/<path>` format.
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
    *   **Source Filtering**: Filter results to a specific content source.
*   **Indexed Fields (Default Boosts)**:
    *   `uri` (Stored, Indexed)
    *   `name` (Stored, Indexed, Boost x2.0)
    *   `content` (Stored, Indexed, Boost x1.0)
    *   `keywords` (Indexed, Boost x3.0, Optional)
    *   `source` (Stored, Indexed as keyword - not analyzed)
