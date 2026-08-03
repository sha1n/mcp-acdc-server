# Authoring Resource Files

This guide explains how to create and structure markdown resource files for the ACDC MCP server.

## Zero-Config Defaults (No Manifest)

`mcp-metadata.yaml` is optional. Point `--content-dir` at any repository that has no manifest and the server starts anyway, indexing `README.md` and `docs/**/*.md` with a built-in server name, version, and instructions naming the repository. Because the manifest is missing (not merely lacking an `index` block), this reuses the same discovery mechanism as [Configured Chunk Indexing](#configured-chunk-indexing-index) — frontmatter is optional — but tolerates conditions that would otherwise fail startup: an empty selection or a file that cannot be read or parsed is logged as a warning and skipped rather than failing the server. See [Content Repository Structure](SPECIFICATIONS.md#content-repository-structure) in the specification for the exact activation rule, default patterns, and derived identity.

Add an `mcp-metadata.yaml` file to override any of this: give the server a custom name, version, and instructions, restrict or expand which files are indexed, or opt back into the stricter legacy layout described below. The rest of this guide assumes a manifest is present.

## File Location

By default (when `mcp-metadata.yaml` has no `index` block — see [Configured Chunk Indexing](#configured-chunk-indexing-index)), place all resource markdown files inside the `mcp-resources/` subdirectory of your content directory:

```
content/
├── mcp-metadata.yaml
└── mcp-resources/
    ├── getting-started.md
    ├── api/
    │   └── endpoints.md
    └── guides/
        └── deployment.md
```

## Server Metadata (`mcp-metadata.yaml`)

The `mcp-metadata.yaml` file in the root of your content directory overrides the [built-in zero-config defaults](#zero-config-defaults-no-manifest): it defines server identity and instructions explicitly, and controls tool descriptions and indexing. The file is optional — omit it to run with defaults — but once present, the fields below are required within it, and an invalid or unreadable manifest fails startup.

### Structure

```yaml
server:
  name: "My Knowledge Base"
  version: "1.0.0"
  instructions: |
    You are an assistant with access to documentation resources.
    Use the search tool to find relevant information before answering.
    Always cite resources by their URI when referencing them.
```

### Server Section

| Field          | Required | Description                                                |
| -------------- | -------- | ---------------------------------------------------------- |
| `name`         | Yes      | Display name for the MCP server                            |
| `version`      | Yes      | Server version string                                      |
| `instructions` | Yes      | System prompt instructions for AI agents                   |

#### The `instructions` Field

The `instructions` field is particularly important—it is delivered to the client verbatim as the MCP server's `instructions`, giving AI agents **system-level guidance** on how to use this server effectively. Well-crafted instructions significantly improve agent behavior.

**What to include:**

- **Context**: Describe what kind of information the server provides
- **Usage guidance**: When and how agents should use the available tools
- **Citation expectations**: How agents should reference resources in responses
- **Constraints**: Any limitations or considerations agents should be aware of

**Example instructions:**

```yaml
instructions: |
  You have access to a technical documentation knowledge base.
  
  SEARCH FIRST: Before answering technical questions, use the search tool
  to find relevant documentation. Search with specific terms.
  
  CITE SOURCES: When referencing information from resources, include
  the resource URI so users can verify the information.
  
  COVERAGE: This knowledge base covers API documentation, deployment
  guides, and troubleshooting. It does not cover billing or account
  management topics.
```

### Tools Section

The tools section allows overriding metadata for the server's available tools (`search` and `read`). If this section is omitted, the server provides high-quality default descriptions for these tools. 

You might want to override these defaults to provide more specific instructions for your AI agents, such as adding examples tailored to your content or adjusting the tool's perceived scope to better fit your domain.

If you provide a tool in this section, it requires:

| Field         | Required | Description                              |
| ------------- | -------- | ---------------------------------------- |
| `name`        | Yes      | Tool identifier (must be unique)         |
| `description` | Yes      | Human-readable description of the tool   |

### Validation

The server validates `mcp-metadata.yaml` at startup and will fail to start if:
- `server.name` is missing or empty
- `server.version` is missing or empty
- `server.instructions` is missing or empty
- Any tool defined in the `tools` section is missing a `name` or `description`
- Duplicate tool names exist

## Chunking and Fragment URIs

Every discovered document — whether found via legacy `mcp-resources/` scanning or a configured `index` (see below) — is split into chunks before indexing, and the `search` tool always returns chunk URIs (`<document-uri>#<fragment>`), never bare document URIs. This section applies to both discovery modes.

### How Documents Are Split

A new chunk starts at *every* Markdown heading, regardless of level — a chunk's content never includes its subsections' content. For example:

```markdown
# Configuration
Parent text.

## Authentication
API keys.
```

produces **two** chunks: one for `# Configuration` containing only "Parent text.", and a separate one for `## Authentication` containing "API keys." The `## Authentication` chunk's heading path is still `["Configuration", "Authentication"]`, so its provenance reflects the nesting even though its content does not include the parent section's body.

Content before the first heading (if any) forms its own chunk, addressed with the reserved fragment `document`. A heading whose text has no letters or digits (e.g. `# !!!`) falls back to the fragment `section`.

A section that exceeds a soft limit of **4,000 Unicode code points** is further split along block boundaries into multiple parts, each still tagged with the section's full heading path.

### Fragment URIs

- An unsplit section is addressable as `<document-uri>#<heading-slug>` (e.g. `acdc://docs/guide#rotating-keys`, or `acdc://docs/guide#document` for the pre-heading preamble).
- A section split into multiple parts addresses each part as `<document-uri>#<heading-slug>~1`, `#<heading-slug>~2`, and so on — the suffix applies to every part, including the first.
- Reading the bare `<document-uri>#<heading-slug>` of a *split* section returns the full section, reconstructed by joining all of its parts.
- Duplicate heading text within the same document produces de-duplicated slugs (e.g. a second "Overview" heading becomes `#overview-1`).

### Result Diversity

To keep results varied across documents, the `search` tool over-fetches candidate chunks internally and then applies a per-document cap before returning up to the configured limit: `references` mode keeps at most one chunk per source document; `content` mode keeps at most two.

The cap discards candidates, so the over-fetch adapts rather than being fixed. The candidate window starts at 5x the result limit and widens 4x for up to two further retrievals (20x, then 80x) while the page is still short and candidates remain. A document with many matching sections therefore costs the page nothing: its extra chunks are replaced by other documents rather than leaving gaps. A page can still come back short — when the corpus genuinely holds fewer matching documents than the limit, or when one document's matching sections outnumber the widest candidate window.

### Fragment Reads

The `read` tool resolves any URI returned by `search` or `resources/list`:

- The base document URI (e.g. `acdc://docs/guide`) returns the full document (frontmatter stripped).
- A chunk fragment URI (e.g. `acdc://docs/guide#rotating-keys~2`) returns only that chunk's content.
- A section fragment URI for a split section (e.g. `acdc://docs/guide#rotating-keys`) returns all of that section's parts joined together.

Only source documents (not individual chunks) appear in `resources/list`; chunk and section URIs are reachable once you have one, typically from a `search` result.

### Path Labels

Each chunk's search ranking also considers **path labels**, derived from the document's relative path: the path (extension stripped) is split on `/`, `-`, `_`, whitespace, and camelCase boundaries, lowercased, and deduplicated. For example, `docs/apiReference/authGuide.md` produces labels `["docs", "api", "reference", "auth", "guide"]`. Path labels are indexed with a 1.25x boost, so file and directory names that match query terms help a document rank higher — see [Keywords and Search Boosting](#keywords-and-search-boosting).

### Duplicate Identity

Regardless of discovery mode, server startup fails if two discovered documents or chunks resolve to the same URI or ID — for example, `guide.md` and `guide.markdown` sitting side by side and both being selected produces the identical document URI `acdc://guide` for both.

## Configured Chunk Indexing (`index`)

By default the server discovers documents under `mcp-resources/`, each requiring frontmatter (see [Resource Frontmatter Format](#resource-frontmatter-format) below). Adding an `index` block to `mcp-metadata.yaml` switches **document discovery** to configured indexing instead: any Markdown matched by glob patterns is discovered directly, without requiring the `mcp-resources/` layout or frontmatter. When `index` is present, it replaces `mcp-resources/` discovery entirely — the two modes are not combined. Discovered documents are chunked and searched identically either way — see [Chunking and Fragment URIs](#chunking-and-fragment-uris) above.

### Schema

```yaml
index:
  include:
    - docs/**/*.md
  exclude:
    - docs/generated/**
```

| Field     | Required | Description                                              |
| --------- | -------- | ---------------------------------------------------------- |
| `include` | Yes      | List of glob patterns selecting Markdown files. At least one pattern is required. |
| `exclude` | No       | List of glob patterns to skip, evaluated after `include`.  |

### Pattern Semantics

- Patterns are relative to `--content-dir`. Absolute patterns or patterns that escape the content root (e.g. `../x.md`) fail at startup.
- Patterns use [doublestar](https://github.com/bmatcuk/doublestar) glob syntax, where `**` matches zero or more path segments. `docs/**/*.md` therefore matches files directly under `docs/` (e.g. `docs/guide.md`) as well as nested files (e.g. `docs/api/auth.md`) — a "zero-depth" match.
- `exclude` always wins: a file matched by `include` is dropped if it also matches any `exclude` pattern.
- Every selected file must have a `.md` or `.markdown` extension; a pattern that selects any other file type fails startup.
- Symlinked files and directories are skipped during discovery.

### Metadata Derivation for Configured Markdown

Frontmatter is **optional** for configured Markdown. When present, it is still parsed as YAML and must be valid. Title, description, and keywords are derived as follows:

| Field         | Source                                                                 |
| ------------- | ----------------------------------------------------------------------- |
| `name`        | Frontmatter `name`, or the document's first `#` (H1) heading if absent. A document whose first heading is `##` or deeper has no fallback and gets an empty name. |
| `description` | Frontmatter `description`, or the document's first paragraph if absent. Always truncated to 200 Unicode characters, whether it came from frontmatter or the fallback. |
| `keywords`    | Frontmatter `keywords` list. Empty if absent — no fallback derivation.  |

### Strict Configured Discovery Errors

Unlike legacy `mcp-resources/` discovery, configured indexing fails the server at startup — rather than skipping the offending file — if any of the following occur:

- `index.include` is missing or empty.
- Any `include`/`exclude` pattern is absolute, escapes the content root, or is not a valid glob.
- No files match the configured patterns.
- A matched file is not `.md`/`.markdown`.
- A matched file cannot be read or fails to parse (including invalid frontmatter YAML, when frontmatter is present).
- The content root itself cannot be resolved (e.g. a broken symlink at `--content-dir`).

See also [Duplicate Identity](#duplicate-identity) above, which fails startup in both discovery modes.

### Unchanged Legacy Behavior

When `index` is absent from `mcp-metadata.yaml`, discovery keeps scanning `mcp-resources/` for `.md` and `.markdown` files exactly as before: frontmatter with `name` and `description` remains required per file, and files that are missing, invalid, or incomplete are skipped with a warning log rather than failing startup. A missing `mcp-resources/` directory yields zero resources, not an error.

### Not Yet Supported

Persistence across restarts is not part of this release: the chunk catalog and search index are rebuilt fully at startup, in memory or a temporary directory, with no watcher process behind it. Content changes are still picked up without a restart, though — a `search` call, a `read` call, or a resource read re-checks the content root (debounced to at most once every 2 seconds) and, only when something actually changed, re-assembles the catalog and rebuilds the index in place. See [Content Refresh](SPECIFICATIONS.md#content-refresh) for the trigger, the debounce, and how its error handling deliberately differs from the strict startup rules above.

See [Configuration Reference](configuration.md) for the `--search-result-mode` flag that controls how much chunk detail the `search` tool returns, and the [Repository Docs example](../examples/repository-docs/) for a runnable configured-indexing setup.

## Resource Frontmatter Format

Legacy `mcp-resources/` discovery (used when `index` is absent from `mcp-metadata.yaml`) requires each resource file to start with YAML frontmatter containing required metadata:

```yaml
---
name: "Resource Title"
description: "A brief description of what this resource contains"
keywords:
  - keyword1
  - keyword2
  - keyword3
---

# Your Markdown Content

The actual content of your resource goes here...
```

### Required Fields

| Field         | Type   | Description                                      |
| ------------- | ------ | ------------------------------------------------ |
| `name`        | string | Display name for the resource                    |
| `description` | string | Brief description shown in resource listings     |

### Optional Fields

| Field      | Type     | Description                             |
| ---------- | -------- | --------------------------------------- |
| `keywords` | string[] | List of keywords for search boosting    |

## Keywords and Search Boosting

Keywords provide a way to improve search relevance. When a search query matches a keyword, that document receives a **3x score boost** (configurable) compared to matches in regular content.

### How It Works

The search service uses a disjunction query across five fields:

| Field         | Boost | Description                                              |
| ------------- | ----- | --------------------------------------------------------- |
| `source_title`| 2.0x  | Resource/document title (configurable)                    |
| `heading_path`| 2.5x  | The chunk's Markdown heading ancestry                      |
| `path_labels` | 1.25x | Labels derived from the file path — see [Path Labels](#path-labels) |
| `content`     | 1.0x  | Markdown chunk content (configurable)                      |
| `keywords`    | 3.0x  | Frontmatter keywords (configurable)                        |

### Advanced Search Features

ACDC implements several features to improve search accuracy for both humans and AI agents:

- **Stemming**: Powered by the English analyzer, it matches different word forms (e.g., "searching" matches "search").
- **Fuzzy Matching**: Tolerates minor typos (e.g., "resouce" matches "resource").
- **Dynamic Highlights**: For agents, we provide contextual snippets around the match to help them reason about relevance without reading the whole resource.

### Example

Given two resources with identical content:

**Resource A** (no keywords):
```yaml
---
name: "Programming Guide"
description: "General programming documentation"
---
Content about software development...
```

**Resource B** (with keywords):
```yaml
---
name: "Programming Guide" 
description: "General programming documentation"
keywords:
  - golang
  - go
  - development
---
Content about software development...
```

Searching for `"golang"` will rank **Resource B** higher because:
1. Resource B matches "golang" in its keywords field (boosted 3x)
2. Resource A has no keyword match

### Best Practices for Keywords

1. **Be specific**: Choose keywords that accurately represent the resource content
2. **Include synonyms**: Add alternative terms users might search for
3. **Keep it focused**: 3-7 keywords is typically sufficient
4. **Use lowercase**: Keywords are analyzed with standard tokenization

```yaml
keywords:
  - api
  - rest
  - http
  - authentication
  - oauth
```

## URI Generation

Resource URIs are automatically generated from the file path using the configured URI scheme (default: `acdc`):

| File Path                           | Generated URI              |
| ----------------------------------- | -------------------------- |
| `mcp-resources/guide.md`            | `acdc://guide`             |
| `mcp-resources/api/endpoints.md`    | `acdc://api/endpoints`     |
| `mcp-resources/docs/setup/intro.md` | `acdc://docs/setup/intro`  |

The URI scheme can be customized via the `--uri-scheme` flag or `ACDC_MCP_URI_SCHEME` environment variable. For example, with `--uri-scheme myorg`:

| File Path                           | Generated URI              |
| ----------------------------------- | -------------------------- |
| `mcp-resources/guide.md`            | `myorg://guide`            |
| `mcp-resources/api/endpoints.md`    | `myorg://api/endpoints`    |

See [Configuration Reference](configuration.md) for details.

The same scheme applies to [configured chunk indexing](#configured-chunk-indexing-index), except the path is relative to `--content-dir` instead of `mcp-resources/` (e.g. `docs/setup/intro.md` → `acdc://docs/setup/intro`). Chunks within a document are addressed with a `#fragment` suffix on the document URI — see [Fragment Reads](#fragment-reads).

## Complete Example

**File:** `content/mcp-resources/api/authentication.md`

```yaml
---
name: "Authentication Guide"
description: "How to authenticate API requests using tokens and API keys"
keywords:
  - auth
  - authentication
  - api-key
  - bearer
  - token
  - security
---

# Authentication Guide

This document explains the available authentication methods...

## Bearer Tokens

To authenticate using bearer tokens...

## API Keys

API keys can be passed via the `X-API-Key` header...
```

**Result:**
- **URI**: `acdc://api/authentication`
- **Search boost**: Queries matching "auth", "token", "security", etc. will rank this resource higher

## Authoring Prompts

Prompts provide a way to define reusable templates that AI agents can use to perform common tasks. They allow you to capture complex workflows and reasoning patterns as structured templates, ensuring consistency across your team and reducing the cognitive load on agents.

### Why use Prompts?

- **Consistency**: Ensure all team members and agents use the same standard instructions for tasks like code reviews, security audits, or documentation generation.
- **Precision**: Use required arguments to force agents to gather necessary context before execution.
- **Efficiency**: Trigger complex multi-step reasoning with simple "slash commands" in compatible AI clients.
- **Maintainability**: Centralize your team's "prompt engineering" in version-controlled markdown files.

### File Location

Place all prompt markdown files inside the `mcp-prompts/` subdirectory of your content directory:

```
content/
├── mcp-metadata.yaml
├── mcp-resources/
└── mcp-prompts/
    ├── code-review.md
    └── explain-code.md
```

### Prompt Frontmatter Format

Each prompt file must start with YAML frontmatter defining its metadata and arguments:

```yaml
---
name: "Prompt Name"
description: "A description of what this prompt does"
arguments:
  - name: "arg1"
    description: "Description of the first argument"
    required: true
  - name: "arg2"
    description: "Description of the second argument"
    required: false
---
```

#### Fields

| Field         | Type     | Required | Description                                           |
| ------------- | -------- | -------- | ----------------------------------------------------- |
| `name`        | string   | Yes      | Internal identifier and display name for the prompt   |
| `description` | string   | Yes      | Human-readable description shown in prompt listings   |
| `arguments`   | object[] | No       | List of dynamic arguments this prompt accepts        |

#### Argument Fields

| Field         | Type    | Required | Description                                      |
| ------------- | ------- | -------- | ------------------------------------------------ |
| `name`        | string  | Yes      | Argument name used in the template (e.g., `{{.arg1}}`) |
| `description` | string  | Yes      | Description of the argument                      |
| `required`    | boolean | No       | Whether the argument is required (default: `true`) |

### Template Content

The body of the markdown file is the prompt template. You can use standard Go template syntax to inject arguments.

#### Value Injecting
Use `{{.ArgumentName}}` to inject a value:
```markdown
Hello {{.name}}!
```

#### Conditional Logic
Use `if/else` for conditional content:
```markdown
{{if .commit}}
Focus on commit {{.commit}}.
{{else}}
Focus on all local changes.
{{end}}
```

### Slash Commands

In many AI clients (like Claude or Gemini), prompts are surfaced as **Slash Commands**. This provides a powerful way to trigger complex reasoning tasks with simple shortcuts.

For example, a prompt named `code-review` can be triggered by typing `/code-review` in the agent's chat interface. If the prompt defines arguments, the agent will prompt you for them or you can provide them directly.

### Complete Example

**File:** `content/mcp-prompts/code-review.md`

```yaml
---
name: code-review
description: Performs a detailed code review.
arguments:
  - name: commit
    description: The git commit hash to review.
    required: false
  - name: instructions
    description: Additional instructions for the review.
    required: false
---
Please perform a detailed code review with the following context:

{{if .commit}}
1. Focus on the changes introduced in commit `{{.commit}}`.
{{else}}
1. Focus on all currently modified files in the repository.
{{end}}

{{if .instructions}}
**Additional Instructions:**
{{.instructions}}
{{end}}

Please provide feedback on architecture, bugs, and security.
```

**Usage:**
- **Slash Command**: `/code-review`
- **With Arguments**: `/code-review commit: "abc123" instructions: "Focus on performance"`

### Best Practices for Prompts

1. **Clear Descriptions**: Write descriptions that explain *what* the prompt expects and *why* it's useful. This helps agents decide when to use it.
2. **Explicit Arguments**: Use specific names for arguments (e.g., `commit_hash` instead of `val`).
3. **Template Safety**: Remember that `mcp-acdc-server` uses the `missingkey=error` option. Ensure all keys used in the template are either defined in `arguments` or handled with conditional logic.
4. **Markdown Formatting**: Since the output of a prompt is often markdown, use proper formatting in the template to help the agent structure its follow-up response.
5. **Atomic Prompts**: Break complex tasks into smaller, focused prompts (e.g., instead of one "Refactor" prompt, have "Refactor for Performance" and "Refactor for Readability").
