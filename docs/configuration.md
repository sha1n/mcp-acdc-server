# Configuration Reference

The server can be configured using **CLI flags**, **environment variables**, or a **`.env` file**.

## Configuration Priority

When the same setting is specified in multiple places, the following priority applies (highest to lowest):

1. **CLI flags** — Explicit command-line arguments
2. **Environment variables** — Shell environment or exported vars
3. **`.env` file** — Key-value pairs in a `.env` file in the working directory
4. **Defaults** — Built-in fallback values

## General Settings

| CLI Flag | Short | Environment Variable | Description | Default |
|----------|-------|---------------------|-------------|---------|
| `--content-dir` | `-c` | `ACDC_MCP_CONTENT_DIR` | Path to content directory. May point at a repository root with no `.acdc/config.yaml` — see [Authoring Resources Guide](authoring-resources.md#zero-config-defaults-no-manifest). | current working directory |
| `--transport` | `-t` | `ACDC_MCP_TRANSPORT` | Transport type: `stdio` or `sse` | `stdio` |
| `--host` | `-H` | `ACDC_MCP_HOST` | Host for SSE server (SSE mode only) | `0.0.0.0` |
| `--port` | `-p` | `ACDC_MCP_PORT` | Port for SSE server (SSE mode only) | `8080` |
| `--uri-scheme` | `-s` | `ACDC_MCP_URI_SCHEME` | URI scheme for resources (e.g. `acdc`, `myorg`) | `acdc` |
| `--cross-ref` | — | `ACDC_MCP_CROSS_REF` | Transform relative markdown links between resources into resource URIs | `false` |
| `--search-max-results` | `-m` | `ACDC_MCP_SEARCH_MAX_RESULTS` | Maximum search results | `10` |
| `--search-keywords-boost` | — | `ACDC_MCP_SEARCH_KEYWORDS_BOOST` | Boost for keywords matches | `3.0` |
| `--search-heading-boost` | — | `ACDC_MCP_SEARCH_HEADING_BOOST` | Boost for heading path (`heading_path`) matches | `2.5` |
| `--search-title-boost` | — | `ACDC_MCP_SEARCH_TITLE_BOOST` | Boost for document title (`source_title`) matches | `2.0` |
| `--search-path-boost` | — | `ACDC_MCP_SEARCH_PATH_BOOST` | Boost for path label (`path_labels`) matches | `1.25` |
| `--search-content-boost` | — | `ACDC_MCP_SEARCH_CONTENT_BOOST` | Boost for content matches | `1.0` |
| `--search-result-mode` | — | `ACDC_MCP_SEARCH_RESULT_MODE` | Search output detail: `references` (chunk citations only) or `content` (citations plus the full matched chunk body) | `references` |
| `--search-in-memory` | — | `ACDC_MCP_SEARCH_IN_MEMORY` | Hold the search index in memory instead of on disk | `true` |
| `--search-semantic-model` | — | `ACDC_MCP_SEARCH_SEMANTIC_MODEL` | Path to a semantic embedding model. Empty disables semantic search | _(empty)_ |
| `--search-semantic-floor` | — | `ACDC_MCP_SEARCH_SEMANTIC_FLOOR` | Minimum cosine similarity a semantic hit must clear to be returned. `-1` disables the floor | `0.25` |

### What the search boosts actually do

A boost is a relative weight within a single normalized query, not a ranking override. What
raising one buys depends on whether that field is already winning. Raising the boost of a
field that is losing narrows its gap to the others and then converges — the scores approach
each other without ever crossing, so no value makes it overtake. Raising the boost of a field
that already leads widens its score margin without changing its position, because there is no
position above first. Field length normalization compounds this: a short `heading_path` match
stays ahead of a long body match largely independently of the weights. Expect to need a change
of roughly an order of magnitude before any ranking moves, and expect some orderings not to be
reachable through boosts at all.

Setting a boost to `0` is different in kind: it removes that field from the query entirely
rather than just weighting it down. Whether that makes a document unreachable depends on
whether the same words also live in another indexed field:

- `--search-path-boost 0` genuinely removes a retrieval route. Path labels come from the file
  path and are not duplicated anywhere else, so documents matched only by their path stop
  being returned.
- `--search-heading-boost 0` demotes rather than hides. A chunk's body text begins at its own
  heading line, so heading words are also indexed as content and the document is still
  retrieved — lower down. That holds only for a section's first chunk: when a long section is
  split into multiple chunks, the later ones carry the section's `heading_path` but not its
  heading line in their content, so for those chunks `--search-heading-boost 0` does hide
  rather than demote.

Two of the five fields carry no signal on plain repository Markdown. `keywords` comes only
from YAML frontmatter, and without frontmatter a document's `source_title` is its first `#`
heading — which is already the head of every chunk's `heading_path`. So in the default
zero-config mode, `--search-heading-boost`, `--search-path-boost`, and `--search-content-boost`
are the three boosts that discriminate between documents; `--search-keywords-boost` and
`--search-title-boost` have nothing to act on until a document supplies frontmatter.

Negative, `NaN`, infinite and unparseable boost values are rejected at startup, as are values
above roughly `1.34e154`. That cap is a conservative sanity check, not a guarantee that scoring
stays sound below it: Bleve squares boost × idf, not the boost alone, so on a real corpus a
boost far below the cap can already collapse every score to zero. The cap only rejects
magnitudes no meaningful ranking could use; the boundary itself is accepted, and only values
strictly above it are rejected. Setting *all five* boosts to `0` is also rejected: the query
would carry no clauses at all, so every search would return nothing while reporting success.
Zeroing any subset of the five remains supported.

### Semantic search

Semantic search is off by default and costs nothing — in dependencies, binary
size, startup time or ranking — until `--search-semantic-model` names a model
path.

When it is set, the server embeds every indexed chunk and fuses the semantic
ranking with the existing full-text ranking using Reciprocal Rank Fusion, so a
query phrased differently from the corpus still finds the right chunk. Chunk
boundaries, chunk URIs and the `search` and `read` tool schemas are unchanged
either way; only result order and the reported score change. Fused scores are
normalized so the top result reads `1.00`, because Reciprocal Rank Fusion
values carry rank information, not magnitude.

A model path that is missing, unreadable, or fails validation aborts startup
with an error naming the path. Configuring semantic search and silently
running without it would hide a broken deployment. A single document that
cannot be embedded is different: it is skipped, counted in one summary warning
at the end of indexing, and stays findable by full-text search.

No embedding backend ships yet, so any configured path currently aborts startup
with "no embedding backend is available in this build".

#### Tuning the similarity floor

Cosine similarity always ranks something first: a vector search returns the
least-dissimilar chunk in the corpus no matter what you ask. `--search-semantic-floor`
is the threshold below which a semantic hit is discarded instead of returned, and it is
what preserves the `No results found` answer for a query nothing matches.

The shipped default is conservative rather than measured, because the threshold
separating a real match from a coincidentally-nearby one is a property of the embedding
model interacting with your corpus. Raise it if unrelated chunks appear in results;
lower it if paraphrased queries stop finding documents that full-text search also
misses. `-1` disables the floor entirely, and values above `1` are rejected at startup
because nothing could ever clear them.

Valid values run from `-1` to `1`. Anything outside that range aborts startup.

### How closely a query has to match

Four of the five indexed fields tolerate a single-character difference between a query term and
an indexed term, so a mistyped `authentcation` still finds the authentication page. `path_labels`
does not: it is matched exactly.

The asymmetry is deliberate. Path labels are a small vocabulary of short path segments, and they
are the only field whose terms appear nowhere else in the index — heading text is also indexed as
content, but a path label comes from the file path alone. Nearly every segment therefore has a
neighbour within one edit, so tolerating differences there retrieves documents on words they do
not contain: a query for `readiness` matched the path label `readme` and filled ranks 2 through 5
of the result page with README chunks that never mention it.

Tolerance is narrow, and it applies to *stems* rather than to the words you typed — the English
analyzer reduces both the query term and the indexed term before they are compared. How much a
given misspelling gets rescued therefore depends on whether it still stems. `authentcation`
finds the authentication page and `cheksums` finds the checksums section, but `deploymnet` does
not find `deployment`: it fails to stem at all, which leaves it four edits from that word's
`deploy` stem rather than the one edit the raw spellings suggest. Bleve measures plain
Levenshtein distance, so swapping two adjacent characters costs two edits, not one. Nor does
this tolerance bridge spelling variants such as `authorisation` against `authorization`. Plural
and tense differences are handled by the analyzer before matching, not by this tolerance at all.

Two alternatives were measured and rejected. A minimum matching prefix does not help: the terms
that collide here already share their first four characters. Bleve's automatic mode is worse — it
raises the tolerance to two characters for any term longer than five, which widens exactly the
problem described above.

### How much of a query has to match

A document is retrieved when it matches **any one** term of the query. Ordering the rest is left to
scoring: matching more terms of the query generally ranks a document higher, though a single rare
term can still outscore several common ones. A search for `helm chart readiness` finds the Helm
section on two of its terms and the readiness section on one, and ranks them in that order.

Requiring *every* term was measured and rejected. Bleve applies that requirement within a single
field rather than across the document, and two of the three fields that discriminate between
documents without frontmatter — the heading path and the path labels — are a handful of words each.
They can almost never hold a whole query, so requiring every term silently removes those two from
the ranking and leaves the document body as the only field that can still match. Measured across
21 queries on two corpora, that changed the leading result on eight and improved none; on a small
documentation set it made ordinary multi-word queries return nothing at all.

### Where the index lives

The search index is held in memory by default. It is rebuilt from the content
directory at startup and on every refresh, and it is never reopened, so
persisting it to disk buys nothing back.

Pass `--search-in-memory=false`, or set `ACDC_MCP_SEARCH_IN_MEMORY=false`, to
build it on disk instead, under a temporary directory.

The figures below were measured at 20,678 chunks — a 10,339-chunk corpus
duplicated, which doubles posting-list length but leaves the term dictionary at
its original size.

A segment is an independent sub-index that every query must search in turn, so
query latency grows with how many an index holds. An in-memory index
accumulates one per indexing batch and never merges them, which is why the
batch size is tuned to keep that count small: it settles at 21 segments and a
4.87 ms query, against 207 segments and 9.21 ms had batches been ten times
smaller.

At that size in-memory builds about 1.3× faster than on disk (1.90 s against
2.42 s) and answers queries only about 10% slower (4.87 ms against 4.41 ms).
On-disk's remaining edge is its merger, which leaves it with fewer segments
still (10 against 21); at equal segment counts the two kinds measure
indistinguishably.

These figures time the search engine answering one query, below the MCP tool
clients call. That tool retries — up to twice, with a wider candidate window —
only if the result page is still short and the candidate set was truncated;
treat these figures as a floor, not an end-to-end number.

`--search-in-memory=false` also pays off when Go heap matters more than
query speed: the on-disk index holds its data in memory-mapped segments the
operating system can reclaim under pressure, where in-memory's Go heap runs
roughly 100 MiB at that size against single-digit MiB on disk.

Size a deployment for twice the index's own memory footprint — twice that
roughly 100 MiB at the scale above, for an in-memory index. A refresh builds the
replacement index in full before releasing the previous one, so a content
change briefly holds two complete indexes at once.

## Authentication Settings

| CLI Flag | Short | Environment Variable | Description | Default |
|----------|-------|---------------------|-------------|---------|
| `--auth-type` | `-a` | `ACDC_MCP_AUTH_TYPE` | Auth type: `none`, `basic`, or `apikey` | `none` |
| `--auth-basic-username` | `-u` | `ACDC_MCP_AUTH_BASIC_USERNAME` | Basic auth username | — |
| `--auth-basic-password` | `-P` | `ACDC_MCP_AUTH_BASIC_PASSWORD` | Basic auth password | — |
| `--auth-api-keys` | `-k` | `ACDC_MCP_AUTH_API_KEYS` | Comma-separated API keys | — |

## Examples

**CLI flags (stdio mode - default):**
```bash
./bin/acdc-mcp -c /path/to/content
```

**CLI flags (SSE mode):**
```bash
./bin/acdc-mcp -t sse --port 9000
```

**CLI flags (SSE with basic auth):**
```bash
./bin/acdc-mcp -t sse --port 9000 --auth-type basic -u admin -P secret
```

**CLI flags (custom URI scheme):**
```bash
./bin/acdc-mcp -c /path/to/content --uri-scheme myorg
```

This produces resource URIs like `myorg://guides/getting-started` instead of the default `acdc://guides/getting-started`.

**CLI flags (content result mode):**
```bash
./bin/acdc-mcp -c /path/to/content --search-result-mode content
```

By default (`references`), the `search` tool returns chunk citations — title, URI, breadcrumb, and a highlighted snippet — so agents follow up with a `read` call for the full chunk. Setting `--search-result-mode content` (or `ACDC_MCP_SEARCH_RESULT_MODE=content`) additionally inlines the full matched chunk body in the search response itself. See the [Authoring Resources Guide](authoring-resources.md#configured-chunk-indexing-index) for how content is split into chunks.

**Environment variables:**
```bash
ACDC_MCP_TRANSPORT=sse ACDC_MCP_CONTENT_DIR=/data ./bin/acdc-mcp
```

**Using a `.env` file:**
```env
transport=sse
port=9000
auth.type=basic
auth.basic.username=admin
auth.basic.password=secret
```

## Configuration Validation

The server validates configuration at startup and will fail with a clear error if:

- `--uri-scheme` is empty or doesn't match RFC 3986 (must start with a letter, then letters/digits/`+`/`-`/`.`)
- `--search-result-mode` is set to anything other than `references` or `content`
- every search boost is `0`, which would leave the query with no clauses and silently return no results
- a search boost exceeds `1.34e154`, a conservative sanity cap beyond which no meaningful ranking
  could use the value (smaller boosts can still degenerate scoring — see above)
- `--auth-type=basic` is set without username/password
- `--auth-type=apikey` is set without API keys
- `--auth-type=none` is set with auth credentials (conflicting intent)
- `--auth-type=basic` is combined with `--auth-api-keys` (mutually exclusive)

API keys must be provided via the `X-API-Key` header in HTTP requests.

> [!CAUTION]
> **Security Best Practices:**
> - Never commit credentials to version control. Ensure `.env` files are in `.gitignore`.
> - Use a secrets manager (e.g., HashiCorp Vault, AWS Secrets Manager) in production.
> - For containerized deployments, use Kubernetes Secrets or Docker secrets.
> - Rotate credentials regularly and use strong, unique passwords/keys.
