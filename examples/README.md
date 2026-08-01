# MCP ACDC Examples

This directory contains examples for deploying and running the MCP ACDC server. These examples demonstrate common patterns for loading content, using both Docker and configured chunk indexing over plain directories.

## 📁 Available Patterns

Choose the pattern that best fits your workflow:

### 1. Local Content Path (Easiest for development)
**Location:** [`docker-local-content/`](docker-local-content/)

This example uses a direct Docker volume mount to share local markdown files with the server. It's the fastest way to iterate on your own resources.

- **URL**: `http://localhost:8080/sse`
- **Pattern**: Direct volume mount of a local directory.
- **Guide**: [Local Content Guide](docker-local-content/README.md)

### 2. Pre-packaged Image (Production-like)
**Location:** [`docker-image-content/`](docker-image-content/)

This example demonstrates how to use a pre-built content image (`sha1n/mcp-acdc-content`) and an init container to populate the server's content.

- **URL**: `http://localhost:8080/sse`
- **Pattern**: Init container with a remote content image.
- **Guide**: [Remote Image Guide](docker-image-content/README.md)

### 3. Repository Docs (Configured Chunk Indexing)
**Location:** [`repository-docs/`](repository-docs/)

This example indexes an existing repository's `docs/` directory directly via the `index` block in `mcp-metadata.yaml`, instead of curating a dedicated `mcp-resources/` tree. No Docker required — run `acdc-mcp` directly against the target repository.

- **Pattern**: Configured chunk indexing (`index.include`/`index.exclude`) over an arbitrary directory.
- **Guide**: [Repository Docs Guide](repository-docs/README.md)

---

## 📂 Sample Content

The Docker examples (`docker-local-content/` and `docker-image-content/`) use the sample content found in the [**`sample-content/`**](sample-content/) directory:
- `mcp-metadata.yaml`: Server identity, instructions, and tool-description overrides.
- `mcp-resources/`: Markdown files (with frontmatter and keywords) that the agent can search and read.
- `mcp-prompts/`: Parameterised prompt templates the agent can invoke.

`repository-docs/` is self-contained and instead points at an existing repository's own `docs/` directory — see its guide for setup.

## 📖 Related Guides

- [Authoring Resources Guide](../docs/authoring-resources.md) — Learn how to format your markdown for the best search results.
- [Configuration Reference](../docs/configuration.md) — Full list of environment variables and flags.
