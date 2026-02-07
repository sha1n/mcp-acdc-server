---
name: "Getting Started"
description: "A quick guide to getting started with MCP ACDC"
---

# Getting Started with MCP ACDC

Welcome to the **Agent Content Discovery Companion (ACDC)** server!

ACDC turns a directory of Markdown files into a fully searchable MCP resource server that any AI agent can query.

## Key Concepts

- **Resources** — Markdown files with YAML frontmatter. They live under `mcp-resources/` and are exposed via the `resources/list` MCP method.
- **Prompts** — Template files under `mcp-prompts/` that provide reusable, parameterised instructions an agent can invoke.
- **Search** — Full-text search powered by Bleve. Every resource is indexed automatically; add `keywords` in frontmatter to boost discoverability.
- **Metadata** — The `mcp-metadata.yaml` file at the content root defines server identity, instructions, and optional tool-description overrides.

## Your First Steps

1. Browse the resources in this sample content directory to see how frontmatter and Markdown work together.
2. Use the `search` tool to find content — try searching for *"configuration"* or *"keywords"*.
3. Invoke one of the sample prompts (e.g., `explain-topic` with `topic: resources`) to see templated prompts in action.

## Learn More

- [Authoring Content](authoring-content.md) — How to write resources and prompts.
- [Configuration Reference](../reference/configuration.md) — CLI flags, environment variables, and authentication.
- [Search Features](../reference/search-features.md) — How the search engine works and how to optimise for it.
