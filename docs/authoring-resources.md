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

Keywords provide a way to improve search relevance. When a search query matches a keyword, that document receives a **2x score boost** compared to matches in regular content.

### How It Works

The search service uses a disjunction query across three fields:

| Field      | Boost | Description                              |
| ---------- | ----- | ---------------------------------------- |
| `name`     | 1.0x  | Resource title                           |
| `content`  | 1.0x  | Markdown body content                    |
| `keywords` | 2.0x  | Frontmatter keywords get boosted scores  |

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

Resource URIs are automatically generated from the file path:

| File Path                           | Generated URI              |
| ----------------------------------- | -------------------------- |
| `mcp-resources/guide.md`            | `acdc://guide`             |
| `mcp-resources/api/endpoints.md`    | `acdc://api/endpoints`     |
| `mcp-resources/docs/setup/intro.md` | `acdc://docs/setup/intro`  |

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
