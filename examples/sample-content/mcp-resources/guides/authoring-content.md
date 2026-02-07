---
name: "Authoring Content"
description: "How to write resources and prompts for ACDC"
keywords: [resources, prompts, frontmatter, markdown, authoring]
---

# Authoring Content for ACDC

This guide explains how to create resources and prompts that the ACDC server can serve to AI agents.

## Writing Resources

Resources are Markdown files stored under `mcp-resources/`. Each file needs YAML frontmatter with at least `name` and `description`:

```yaml
---
name: "My Resource"
description: "A short summary shown in resource listings"
keywords: [optional, search, terms]
---
```

### Frontmatter Fields

| Field | Required | Purpose |
|---|---|---|
| `name` | Yes | Display name shown in `resources/list` |
| `description` | Yes | Short summary for the resource listing |
| `keywords` | No | List of terms that boost this resource in search results |

### Organising Files

Place resources in subdirectories to keep things tidy. The directory structure is reflected in the resource URI:

```
mcp-resources/
  guides/getting-started.md    ->  acdc://guides/getting-started
  reference/configuration.md   ->  acdc://reference/configuration
```

### Cross-Referencing

Link between resources using relative Markdown paths. When the server runs with `--cross-ref`, these links are automatically transformed into `acdc://` URIs so the agent can follow them:

```markdown
See the [Configuration Reference](../reference/configuration.md) for details.
```

## Writing Prompts

Prompts live under `mcp-prompts/` as Markdown files with special frontmatter:

```yaml
---
name: explain-topic
description: "Explains a topic using available resources"
arguments:
  - name: topic
    description: The topic to explain
    required: true
  - name: depth
    description: "brief or detailed"
    required: false
---
```

### Template Syntax

Prompt bodies use Go templates. Reference arguments with `{{.argName}}` and use conditionals:

```
{{if .depth}}Provide a {{.depth}} explanation.{{else}}Provide a concise overview.{{end}}
```

### Referencing Resources

Prompts can direct the agent to read specific resources using their URI:

```
Start by reading `acdc://guides/getting-started`.
```

## Further Reading

- [Getting Started](getting-started.md) — Overview of key concepts.
- [Search Features](../reference/search-features.md) — How keywords affect search ranking.
