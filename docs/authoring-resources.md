# Authoring Resource Files

This guide explains how to create and structure markdown resource files for the ACDC MCP server.

## File Location

Place all resource markdown files inside the `mcp-resources/` subdirectory of your content directory:

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

The `mcp-metadata.yaml` file in the root of your content directory defines server identity and instructions. This file is **required** for the server to start.

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

The `instructions` field is particularly important—it provides **system-level guidance** to AI agents on how to use this server effectively. Well-crafted instructions significantly improve agent behavior.

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

## Resource Frontmatter Format

Each resource file **must** start with YAML frontmatter containing required metadata:

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

The search service uses a disjunction query across three fields:

| Field      | Boost | Description                              |
| ---------- | ----- | ---------------------------------------- |
| `name`     | 2.0x  | Resource title (configurable)            |
| `content`  | 1.0x  | Markdown body content (configurable)     |
| `keywords` | 3.0x  | Frontmatter keywords (configurable)      |

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
1. Resource B matches "golang" in its keywords field (boosted 2x)
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
