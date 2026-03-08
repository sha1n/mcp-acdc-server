---
name: "Search Features"
description: "How the ACDC search engine works and how to optimise for it"
keywords: [search, keywords, boosting, fuzzy, bleve]
---

# Search Features

ACDC uses the [Bleve](https://blevesearch.com/) full-text search engine to index and query resources.

## What Gets Indexed

Every resource is indexed across three fields:

| Field | Source | Boost | Purpose |
|---|---|---|---|
| `name` | Frontmatter `name` | Highest | Matches resource titles first |
| `keywords` | Frontmatter `keywords` list | High | Lets authors tag resources for discoverability |
| `content` | Full Markdown body | Normal | Catches everything else |

## How Searching Works

When an agent calls the `search` tool with a query:

1. The query is matched against all three fields.
2. Results are ranked by relevance, with higher-boost fields contributing more to the score.
3. Each result includes the resource name, URI, and a text snippet showing where the query matched.

## Using Keywords Effectively

Keywords let you control discoverability without cluttering the prose. Good keywords are:

- **Synonyms** the reader might search for (e.g., `env` alongside *environment variables*).
- **Abbreviations** (e.g., `auth` for *authentication*).
- **Related concepts** that aren't mentioned in the body.

```yaml
keywords: [config, env, flags, transport, auth, uri-scheme]
```

Keep the list short — 3 to 8 terms is usually enough.

## Fuzzy and Prefix Matching

Bleve supports fuzzy matching, so minor typos in queries still return relevant results. Prefix matching means searching for `config` can surface documents containing `configuration`.

## Tips for Better Results

1. Write descriptive `name` and `description` fields — they carry the most weight.
2. Add `keywords` for terms that don't appear naturally in the text.
3. Use clear headings and structured Markdown so snippets are meaningful.
4. Keep resources focused on a single topic for precise matches.

## See Also

- [Configuration Reference](configuration.md) — Server settings including search-related flags.
- [Authoring Content](../guides/authoring-content.md) — How to write resources and set keywords.
