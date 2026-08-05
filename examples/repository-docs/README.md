# Repository Docs (Configured Chunk Indexing)

This example shows the other way to point ACDC at content: instead of curating a dedicated `.acdc/resources/` tree with required frontmatter, it indexes an **existing repository's `docs/` directory** directly. Documents are split into heading-aware chunks for search — see the [Authoring Resources Guide](../../docs/authoring-resources.md#configured-chunk-indexing-index) for the full behavior.

Two variants are shown below: running with **no manifest at all** (zero-config defaults), and running with the `.acdc/config.yaml` in this directory to customize identity or the indexed patterns.

## No-Manifest Variant (Zero-Config)

If the repository you're pointing at has no `.acdc/config.yaml`, skip straight to running the server — no setup required:

```bash
acdc-mcp --content-dir /path/to/repository
```

This indexes `README.md` and `docs/**/*.md` under that repository, deriving the server name and instructions from the repository's directory name. Unlike the configured variant below, an empty selection or an unreadable/unparsable file is a startup warning rather than a fatal error — see [Zero-Config Defaults](../../docs/authoring-resources.md#zero-config-defaults-no-manifest).

## Configured Variant (`.acdc/config.yaml`)

Add a manifest when you want a custom server name/instructions, or `index.include`/`index.exclude` patterns other than the `docs/**/*.md` default.

### What this example demonstrates

- metadata index.include selects Markdown relative to --content-dir
- index.exclude wins over include
- frontmatter is optional for configured Markdown
- references is the default result mode
- content mode is selected with --search-result-mode=content
- base URIs read full documents; search-returned fragments read chunks
- existing .acdc/resources behavior remains when index is absent
- persistence across restarts is not part of this release; mid-session content changes are still picked up via debounced refresh

### Usage

1. Copy `.acdc/config.yaml` from this directory to `.acdc/config.yaml` at the root of the repository whose `docs/` you want to search (the same directory you'll pass as `--content-dir`). If that repository already has a `.acdc/config.yaml`, merge the `index` block into it instead of overwriting the file.
2. Adjust `index.include`/`index.exclude` if your documentation lives somewhere other than `docs/`.
3. Run the server pointed at that repository:

```bash
acdc-mcp --content-dir /path/to/repository
acdc-mcp --content-dir /path/to/repository --search-result-mode content
```

The first command returns chunk citations (title, URI, breadcrumb, and a snippet) that agents follow up on with the `read` tool. The second additionally inlines the full matched chunk body in the search response itself.

## Notes (Both Variants)

- No frontmatter is required in the indexed Markdown; `name`/`description` fall back to the first H1 (`#`) heading and first paragraph when frontmatter is absent. A document whose first heading is `##` or deeper gets an empty name.
- The server rebuilds its chunk catalog and search index fully at startup; there is no persistence across restarts. You don't need to restart it to pick up documentation changes, though: a `search` call, a `read` call, or a resource read re-checks the content root (debounced to at most once every 2 seconds) and rebuilds the catalog and index in place when something changed. See [Content Refresh](../../docs/SPECIFICATIONS.md#content-refresh) for the details, including the brief pause a rebuild puts on concurrent searches.
