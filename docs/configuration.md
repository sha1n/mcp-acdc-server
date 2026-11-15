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

## Web UI Settings

| CLI Flag | Short | Environment Variable | Description | Default |
|----------|-------|---------------------|-------------|---------|
| `--ui-enabled` | — | `ACDC_MCP_UI_ENABLED` | Serve the read-only [web UI](web-ui.md) at `/ui/` and its JSON API at `/api/v1/` (SSE mode only) | `false` |
| `--ui-max-results` | — | `ACDC_MCP_UI_MAX_RESULTS` | Default number of web UI search results, `1`–`100`. Independent of `--search-max-results` | `20` |

The `.env` keys are `ui.enabled` and `ui.max_results`. The UI shares the SSE server's host, port, and authentication. Under `--transport stdio`, `--ui-enabled` is ignored and the server logs a warning instead of failing. See [Web UI](web-ui.md) for routes, the API, and authentication behaviour.

## Server Mode

The server mode decides whether a stdio launch joins a **shared server** or runs as a **per-session server**. On macOS and Linux a stdio launch is, by default, a **session proxy**: it relays its session to the one shared server that serves every launch with the same **server identity**, so those sessions share one catalog and one search index. The shared server is a separate, detached process that the first session proxy starts on demand. `--transport sse` is not affected.

To run every stdio launch as a self-contained per-session server, as before, pass `--server-mode session` (or set `ACDC_MCP_SERVER_MODE=session`).

### Server Mode Settings

| CLI Flag | Environment Variable | `.env` Key | Default | Validation |
|----------|---------------------|------------|---------|------------|
| `--server-mode` | `ACDC_MCP_SERVER_MODE` | `server_mode` | `shared`; on Windows every launch behaves as `session` | `shared` or `session`; an unknown value fails |
| `--shared-server-connect-wait` | `ACDC_MCP_SHARED_SERVER_CONNECT_WAIT` | `shared_server.connect_wait` | `5s` | Go duration, greater than `0`, no maximum; a bare number is rejected |
| `--shared-server-linger` | `ACDC_MCP_SHARED_SERVER_LINGER` | `shared_server.linger` | `30s` | Go duration, at least `1s`; a bare number is rejected |

- **Server mode**: `shared` makes a stdio launch a session proxy; `session` makes it a per-session server.
- **Connect wait**: how long a session proxy tries to reach or start a shared server before it gives up. It is always the launching session's own value.
- **Linger**: how long a shared server keeps running with no attached session. There is no value that disables the linger or makes it unlimited.
- Durations are Go duration strings such as `500ms`, `5s` or `1m`. A bare number such as `30` fails with an error naming the expected format.
- None of the three has a short flag.
- All three are validated on every launch, including `--transport sse`, `--server-mode session` and Windows, where they have no effect. `--server-mode shared` with `--transport sse` is accepted and ignored.
- None of the three is part of the server identity: launches that differ only in these settings share a shared server.

> [!IMPORTANT]
> **The linger depends on launch order.** A running shared server keeps, for its whole lifetime, the linger of the session proxy that started it. A later session with a different `--shared-server-linger` joins that shared server and its own value is ignored. No other setting behaves this way.

### How Sessions Share a Shared Server

**Server identity.** Two launches share a shared server only when their server identities are equal. The identity is derived from the settings (other than the three above), the binary version and the content fixed at startup: the manifest (`.acdc/config.yaml`) and the prompts. Every launch reads the manifest and the prompts directory before it looks for a shared server. A different binary version never joins a running shared server; it gets its own. The operating system user is not part of the identity; users are kept apart by the per-user endpoint directory.

**Files and directories.** Each shared server has a socket `<id>.sock` and a lock file `<id>.lock` in the endpoint directory, and a log file `<id>.log` in the log directory, where `<id>` is the server identity.

| | Linux | macOS | Windows |
|---|---|---|---|
| Endpoint directory | `$XDG_RUNTIME_DIR/acdc-mcp/`; if `XDG_RUNTIME_DIR` is unset, `$TMPDIR/acdc-mcp-<uid>/` (`/tmp/acdc-mcp-<uid>/` when `TMPDIR` is unset) | `~/Library/Caches/acdc-mcp/` | none |
| Socket path limit | 107 bytes | 103 bytes | — |
| Log directory | `$XDG_STATE_HOME/acdc-mcp/`, default `~/.local/state/acdc-mcp/` | `~/Library/Logs/acdc-mcp/` | none |

- The endpoint directory must be owned by the current user with mode `0700`. That mode is the socket's only protection: a shared server never applies the authentication settings.
- The log directory is created with mode `0700` and the log file with mode `0600`. A shared server appends to its log, and a shared server started again for the same identity continues the same file. If the directory or file cannot be created or opened, or the log directory is the endpoint directory, the shared server keeps serving without a log.
- Log size and retention are fixed and not configurable, and nothing rotates while a shared server runs. At each shared server startup a `<id>.log` larger than 10 MiB (10,485,760 bytes) is renamed to `<id>.log.1`, replacing any previous one, and `*.log` and `*.log.1` files in the log directory not modified for 7 days are deleted.
- Log entries caused by a session carry `session=<id>`. Nothing from the shared server log is copied to a session proxy's stderr.

**Lifecycle.** A shared server exits once it has had no attached session for the linger, counted from its startup and then from the last session that detached; it then removes its socket. A shared server ended by SIGTERM or SIGINT exits at once, and its session proxies treat that as a crash. A session proxy exits on stdin end of file, and on SIGINT or SIGTERM well inside 500 ms, without waiting on the shared server.

**Session proxy stderr.** A session proxy writes the full settings dump of its own launch, as a per-session server does, and then:

| Line | When |
|------|------|
| `Attached to the shared server` (Info, with `session` and `log`) | on every attach, first connect and each recovery; `Attached to the shared server, which has no log file` (Warn) when the shared server has no log |
| `Shared server unavailable, continuing as a per-session server` (Warn, with `cause`) | the session continues as a per-session server (see below) |
| `Shared server connection lost, recovering` (Warn) | a crash is detected |
| `Recovered the shared server connection` (Info) | a recovery completed |
| `Replayed initialize result differs from the one the host saw` (Warn) | after a recovery, the new shared server reports different capabilities, instructions or server information; the session continues with what the host first saw |

The wording of these lines is not part of the public contract.

**Continuing as a per-session server.** The launch never fails because of the server mode. If, before its session is attached, the session proxy cannot compute the server identity, cannot create or verify the endpoint directory, finds the socket path over the limit, cannot start a shared server, runs out the connect wait, or loses the connection before the shared server is ready twice in a row, it writes the warning above and serves the session itself as a per-session server. Nothing the host sent is lost or reordered.

**Recovery.** A connection lost after the session is attached is a crash. The session proxy computes the server identity again, joins or starts a shared server for the content as it is then, and resumes the session there:

- every request that was waiting for a response is answered with the JSON-RPC error `{"code":-32000,"message":"shared server connection lost before a response was received"}` and is not sent again; the code and message are public contract;
- messages the host sends during the outage are held, in order and without a limit, and sent once recovery completes;
- for a host that opened the session with `initialize`, the handshake and the last `logging/setLevel` are replayed; open `subscriptions/listen` streams are re-opened with the same id;
- the session proxy then sends `notifications/resources/list_changed` and `notifications/prompts/list_changed`, whether or not anything changed: after an `initialize` handshake, each one whose `listChanged` capability the host saw advertised; otherwise, on each re-opened listen stream, those it subscribed to; tools are fixed by the binary and get no announcement;
- because the identity is computed again, a recovered session moves to the current manifest and prompts. This is the only case where a running session's instructions, prompts or tool descriptions change.

There is no limit on successive recoveries. A connection lost while the session proxy writes the change announcements to the host is a new crash and starts another recovery.

**Failed recovery.** A recovery fails when no shared server can be reached or started within the connect wait (or for any other reason that would make a first launch continue as a per-session server), when the connection is lost again before the replay and the re-opened streams complete, or when the new shared server answers the replayed `initialize` with an error or with a different `protocolVersion`. The session proxy then answers every held request with the `-32000` error, in the order received; an open listen stream gets no error, while a held listen request does. This answering has no time limit of its own: only the session ending (stdin end of file or a signal) cuts it short. The session proxy writes no line of its own; the one error line on stderr is the command's `Error: failed to recover the shared server connection (<cause>): ...`, and the exit code is `1`. A session proxy never becomes a per-session server mid-session. Stdin end of file during an outage ends the session proxy normally.

### Disclosures

- A detached shared server process outlives the host session by the linger. It runs in its own session, outside the host's process group, and is started from the same binary with the hidden `--shared-server-identity` flag; that flag is internal and unsupported, with no compatibility promise, and must not be passed by hand.
- A socket and a lock file exist in the per-user runtime or cache directory (the endpoint directory above). The socket is removed when the shared server exits; the lock file stays.
- A log directory exists at the locations above, with the fixed size and retention rules described there.
- The linger comes from whichever session started the shared server.
- Any failure to reach or start a shared server before the session is attached degrades to a per-session server with a warning on stderr.
- Documents refresh exactly as before: a shared server re-checks the content on `search`, `read` and resource reads, at most once every 2 seconds, rebuilds the index in place and sends `notifications/resources/list_changed`.
- An edit to the manifest or a prompt takes effect for sessions launched after it; running sessions keep what they started with, and a second shared server may run alongside the first until those sessions close.
- An attached session may receive `notifications/resources/list_changed` caused by another session's request, and a rebuild briefly blocks `search` for every attached session.
- The one-line opt-out: `--server-mode session`.
- Server mode is introduced in the minor release `v0.9.0`, which is not a breaking change: no existing flag, variable, config key, tool or protocol behaviour is removed or renamed.

### Known Limitations

Where a limitation matters to you, `--server-mode session` avoids it.

**Process lifetime**

- Environments can end a detached shared server regardless of the detach: stopping a systemd unit or scope (`KillMode=control-group`; the new session does not leave the cgroup), logging out with logind `KillUserProcesses=yes`, and a container whose PID 1 exits. Without an init (`docker run --init`), an orphaned shared server lingers as a zombie. Sandboxed hosts are unverified.
- Hosts that end a whole parent-PID tree (VS Code does so after 10 seconds) also end the shared server if the session proxy is still alive at that point; the session proxy's prompt exit on stdin end of file and on signals is what avoids it. Claude Desktop and Cursor termination behaviour is unverified.
- A session proxy starts a shared server without waiting for it. On macOS and Linux a shared server that exits while the session proxy that started it is still running stays in the process table as a defunct entry until that session proxy exits. The number of such entries is bounded by the shared servers one session started.
- **Docker.** The image's default transport is stdio, so `docker run -i` is a session proxy that starts a detached shared server inside the container (endpoint directory `/tmp/acdc-mcp-0/`, log directory `/root/.local/state/acdc-mcp/`). The shared server ends with the container, so it serves more than one session only through `docker exec` into that container. The session proxy is PID 1 and does not reap, so defunct entries last for the container's life. `-e ACDC_MCP_SERVER_MODE=session` restores one process per container.
- A hung but alive shared server is not detected.
- A session that connects while a shared server is shutting down can lose that one attempt: the shared server stops accepting first, then ends its sessions, and writes no ready line while stopping. A session admitted an instant before shutdown began is attached and then sees its connection end, which is a crash and is recovered; a connection that arrives while the shared server is stopping is dropped before the session is attached, and the session proxy's single retry applies.
- A peer that disconnects while the index is being built, or while the shared server re-checks the content for a new session, stays counted as an attached session until the next I/O on its connection, which delays the linger. A request handler or content re-check that does not stop on cancellation delays a signalled exit.

**Server identity and content**

- Two spellings of one content directory on a case-insensitive filesystem get two shared servers.
- A whitespace-only edit to the manifest or a prompt file yields a separate shared server.
- An edit that lands between a session proxy computing the server identity and the shared server reading the content makes that one launch wait out the connect wait and continue as a per-session server.
- `resources/list` triggers no refresh check, so its listing can lag until another call (`search`, `read`, a resource read) runs; this is unchanged behaviour on every server. Only a session's first `resources/list` after it is attached is guaranteed to be current.
- Content fixed at startup is never reloaded in place: a running session keeps the `instructions`, server name, tool descriptions, index include and exclude rules and prompts it started with, except after a recovery.

**Relay and recovery**

- An invalid (non-JSON) host line ends the launch of a per-session server. In shared mode the shared server ends that session instead, recording the decode error in its log, and the session proxy treats it as a lost connection: it writes the crash warning and recovers onto a shared server with a new session identifier. The invalid line is not sent again. With go-sdk v1.8.0 neither mode answers a malformed line with a parse-error response.
- When host stdin ends inside an incomplete line, the session proxy exits normally (code `0`), whereas a per-session server fails with `Error: unexpected EOF` (code `1`).
- If the shared server stops reading a session's connection, the session proxy stops reading host stdin, so a host that closes stdin in that state is not noticed until the write completes; SIGINT or SIGTERM ends the session proxy at once. At stdin end of file, lines already read are still delivered, with a short bound on a blocked write.
- The server issues no requests to the host today. If server-initiated requests are added, a host response to a request issued by a lost shared server would be forwarded to its successor; that must be revisited then.
- Re-opening `subscriptions/listen` streams after a recovery relies on go-sdk accepting a replayed listen request with the same id; a permanent test guards that behaviour across go-sdk upgrades.
- How real hosts react to unsolicited `list_changed` notifications and to latency during an outage is unverified, and no automated test covers it.

**Logging, files and credentials**

- A long-lived shared server can exceed 10 MiB of log before its next startup.
- A live shared server that wrote nothing for 7 days can have its still-open log file deleted by the startup of a shared server with another identity.
- A lost lock and lock I/O errors are not logged; the session proxy sees only a failed attempt to reach a shared server.
- An existing log directory is used with whatever mode it has, a FIFO at `<id>.log` blocks the log open while the shared server holds its lock, and symbolic links among the log directory's ancestors are followed. Likewise for the endpoint directory, the replacement of an ancestor directory and setuid, setgid and sticky bits are not checked.
- Under `search.in_memory=false`, a shared server that crashes or is ended by SIGTERM or SIGINT leaves its `acdc_search_*` directory in the system temp directory, one per occurrence; nothing cleans these up, and they are left to the operating system's temp cleaner.
- Credentials passed through the environment (for example `ACDC_MCP_AUTH_*`) remain in the shared server's environment until it exits, including the linger.

**Test coverage (contributors)**

- Code run in a subprocess contributes no coverage. The in-process tests do not prove that the operating system releases the lock when a process dies; one binary-tier test does.

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

**CLI flags (SSE with the web UI):**
```bash
./bin/acdc-mcp -t sse --ui-enabled --ui-max-results 50
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

**CLI flags (per-session server under stdio):**
```bash
./bin/acdc-mcp -c /path/to/content --server-mode session
```

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
- `--ui-enabled` is set and `--ui-max-results` is outside `1`–`100`. While the UI is disabled, `--ui-max-results` is not validated
- `--server-mode`, `--shared-server-connect-wait` or `--shared-server-linger` is outside the values in [Server Mode Settings](#server-mode-settings); these are checked on every launch

API keys must be provided via the `X-API-Key` header in HTTP requests.

> [!CAUTION]
> **Security Best Practices:**
> - Never commit credentials to version control. Ensure `.env` files are in `.gitignore`.
> - Use a secrets manager (e.g., HashiCorp Vault, AWS Secrets Manager) in production.
> - For containerized deployments, use Kubernetes Secrets or Docker secrets.
> - Rotate credentials regularly and use strong, unique passwords/keys.
