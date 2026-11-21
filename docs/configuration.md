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

`acdc-mcp browse` takes the same settings as an MCP launch and prints the URL of a web UI served by the [shared server](#server-mode) for their [server identity](#server-identity), starting the shared server and its UI on demand, and opens the browser on that URL; `--no-open` only prints it. A browser that cannot be opened is a warning on stderr, not a failure. An open page is a viewer that keeps the shared server alive: with no session attached and no page open, the shared server exits once its [linger](#detached-shared-server-process) runs out. That UI listens on `127.0.0.1` on an ephemeral port with no authentication; `--ui-enabled`, `--host`, `--port` and `auth.*` are ignored, and the `ui.max_results` of the `browse` that started the UI applies for the life of its shared server. Under the `session` server mode, and on Windows, `browse` instead builds the index itself, serves the UI on `127.0.0.1` on an ephemeral port, prints the URL, opens the browser unless `--no-open` is set (on Windows it only prints the URL and logs a warning that the browser was not opened), and runs until `SIGINT` or `SIGTERM`, then exits `0`; it starts no shared server and writes no endpoint files, and exits `1` when the UI cannot bind or the index build fails. `browse` exits `1` under `--transport sse`. See [`acdc-mcp browse`](web-ui.md#acdc-mcp-browse).

## Server Mode

The server mode decides whether a stdio launch joins a **shared server** or runs as a **per-session server**. On macOS and Linux a stdio launch is, by default, a **session proxy**: it relays its session to the one shared server that serves every launch with the same [server identity](#server-identity), so those sessions share one catalog and one search index. The first session proxy that finds no running shared server starts one; see [Detached shared server process](#detached-shared-server-process). `--transport sse` is not affected. On Windows there is no shared server: every stdio launch is a per-session server, nothing is discovered, no file or directory is created and no fallback warning is written.

To return to one self-contained process per launch, see [Opting out](#opting-out).

### Server Mode Settings

| CLI Flag | Environment Variable | `.env` Key | Default | Validation |
|----------|---------------------|------------|---------|------------|
| `--server-mode` | `ACDC_MCP_SERVER_MODE` | `server_mode` | `shared`; on Windows every launch behaves as `session` | `shared` or `session`; an unknown value fails |
| `--shared-server-connect-wait` | `ACDC_MCP_SHARED_SERVER_CONNECT_WAIT` | `shared_server.connect_wait` | `5s` | Go duration, greater than `0`, no maximum; a bare number is rejected |
| `--shared-server-linger` | `ACDC_MCP_SHARED_SERVER_LINGER` | `shared_server.linger` | `30s` | Go duration, at least `1s`; a bare number is rejected |

- **Server mode**: `shared` makes a stdio launch a session proxy; `session` makes it a per-session server.
- **Connect wait**: how long a session proxy keeps trying to reach a shared server, by connecting to its socket or starting one, before it gives up. It bounds only that phase. Once the socket accepts, the wait for the shared server's ready line (which a shared server still building its index sends late) and, during a [recovery](#recovery), the wait for the responses to the replayed requests have no timer: they end only when the session ends or the connection fails. The connect wait is always the launching session's own value.
- **Linger**: when a shared server with no attached session and no live viewer exits is described in [Detached shared server process](#detached-shared-server-process), and which session's value applies in [The linger depends on launch order](#the-linger-depends-on-launch-order). There is no value that disables the linger or makes it unlimited.
- Durations are Go duration strings such as `500ms`, `5s` or `1m`. A bare number such as `30` fails with an error naming the expected format.
- None of the three has a short flag.
- All three are validated on every launch, including `--transport sse`, `--server-mode session` and Windows, where they have no effect. `--server-mode shared` with `--transport sse` is accepted and ignored.

### How Sessions Share a Shared Server

#### Server identity

Two launches share a shared server only when their server identities are equal. The identity is derived from:

- the binary version: a different binary version never joins a running shared server, it gets its own;
- the content fixed at startup, the manifest (`.acdc/config.yaml`) and the prompts, which every launch reads before it looks for a shared server; what an edit to them does is described in [Manifest and prompt edits](#manifest-and-prompt-edits);
- the content directory, resolved to an absolute path with symbolic links followed;
- every other setting, including one added in a later release, except the settings that never distinguish shared servers: `transport`, `host`, `port`, all [authentication settings](#authentication-settings) (`auth.*`), all [Web UI settings](#web-ui-settings) (`ui.*`) and the three [server mode settings](#server-mode-settings). Launches that differ only in these settings share a shared server.

The operating system user is not part of the identity; users are kept apart by the per-user endpoint directory.

#### Files and directories

The [socket and lock file](#socket-and-lock-file) are in the endpoint directory and the [log file](#log-directory) is in the log directory:

| | Linux | macOS | Windows |
|---|---|---|---|
| Endpoint directory | `$XDG_RUNTIME_DIR/acdc-mcp/`; if `XDG_RUNTIME_DIR` is unset, `$TMPDIR/acdc-mcp-<uid>/` (`/tmp/acdc-mcp-<uid>/` when `TMPDIR` is unset) | `~/Library/Caches/acdc-mcp/` | none |
| Socket path limit | 107 bytes | 103 bytes | — |
| Log directory | `$XDG_STATE_HOME/acdc-mcp/`, default `~/.local/state/acdc-mcp/` | `~/Library/Logs/acdc-mcp/` | none |

- The endpoint directory must be owned by the current user with mode `0700`. That mode is the socket's only protection: a shared server never applies the authentication settings.
- The log directory is created with mode `0700` and the log file with mode `0600`. A shared server appends to its log, and a shared server started again for the same identity continues the same file. If the directory or file cannot be created or opened, or the log directory is the endpoint directory, the shared server keeps serving without a log.
- Log entries caused by a session carry `session=<id>`. Nothing from the shared server log is copied to a session proxy's stderr.

#### Lifecycle

A shared server exits on its own as described in [Detached shared server process](#detached-shared-server-process). A shared server ended by SIGTERM or SIGINT exits at once, and its session proxies treat that as a crash. A session proxy exits on stdin end of file, and on SIGINT or SIGTERM well inside 500 ms, without waiting on the shared server.

#### Session proxy stderr

A session proxy writes the full settings dump of its own launch, as a per-session server does, and then:

| Line | When |
|------|------|
| `Attached to the shared server` (Info, with `session` and `log`) | on every attach, first connect and each recovery; `Attached to the shared server, which has no log file` (Warn) when the shared server has no log |
| `Shared server unavailable, continuing as a per-session server` (Warn, with `cause`) | see [Falling back to a per-session server](#falling-back-to-a-per-session-server) |
| `Shared server connection lost, recovering` (Warn) | a crash is detected |
| `Recovered the shared server connection` (Info) | a recovery completed |
| `Replayed initialize result differs from the one the host saw` (Warn) | after a recovery, the new shared server reports different capabilities, instructions or server information; the session continues with what the host first saw |

The wording of these lines is not part of the public contract.

#### Recovery

A connection lost after the session is attached is a crash. The session proxy computes the server identity again, joins or starts a shared server for the content as it is then, and resumes the session there:

- every ordinary request that was waiting for a response is answered with the JSON-RPC error `{"code":-32000,"message":"shared server connection lost before a response was received"}` and is not sent again; the code and message are public contract. Open `subscriptions/listen` requests are the exception: they get no error and are kept, to be re-issued as described below;
- messages the host sends during the outage are held, in order and without a limit, and sent once recovery completes;
- for a host that opened the session with `initialize`, the handshake and the last `logging/setLevel` are replayed; the kept `subscriptions/listen` requests are re-issued with the same id;
- the session proxy then sends `notifications/resources/list_changed` and `notifications/prompts/list_changed`, whether or not anything changed: after an `initialize` handshake, each one whose `listChanged` capability the host saw advertised; otherwise, on each re-opened listen stream, those it subscribed to; tools are fixed by the binary and get no announcement;
- because the identity is computed again, a recovered session moves to the current manifest and prompts; see [Content fixed at startup](#also-on-a-per-session-server).

There is no limit on successive recoveries. A connection lost while the session proxy writes the change announcements to the host is a new crash and starts another recovery. While the session proxy is reaching a shared server or replaying, stdin end of file ends it normally.

#### Failed recovery

A recovery fails when no shared server can be reached or started within the connect wait (or for any other reason that makes a first launch [fall back to a per-session server](#falling-back-to-a-per-session-server)), when the connection is lost again before the replay and the re-opened streams complete, or when the new shared server answers the replayed `initialize` with an error or with a different `protocolVersion` (both with the cause `protocol_version`). The session proxy then answers every held request with the `-32000` error, in the order received; an open listen stream gets no error, while a held listen request does. This answering has no time limit of its own, and stdin end of file does not cut it short: only a signal ending the session does. The session proxy writes no line of its own; the one error line on stderr is the command's `Error: failed to recover the shared server connection (<cause>): ...`, and the exit code is `1`. A session proxy never becomes a per-session server mid-session.

### Disclosures

#### Detached shared server process

A shared server is a detached process that outlives the host session by the linger: it exits once it has had no attached session and no live viewer for the linger, counted from its startup and then from when the last session detached, the last viewer stopped being live or the last `browse` was answered, whichever is later. A viewer is a [web UI](web-ui.md#acdc-mcp-browse) page open on the shared server: the page sends a heartbeat about every 30 seconds and counts as live for 2 minutes after its last one, so a closed page holds off the linger for up to 2 minutes more. It runs in its own session, outside the host's process group, and is started from the same binary with the hidden `--shared-server-identity` flag; that flag is internal and unsupported, with no compatibility promise, and must not be passed by hand. Environments that can end it earlier are listed under [Process lifetime](#process-lifetime).

#### Socket and lock file

Each shared server has a socket `<id>.sock` and a lock file `<id>.lock`, where `<id>` is the server identity, in the per-user endpoint directory: a runtime directory on Linux, a cache directory on macOS (locations in [Files and directories](#files-and-directories)). The socket is removed when the shared server exits; the lock file stays.

#### Log directory

Each shared server logs to `<id>.log` in the log directory at the platform locations in [Files and directories](#files-and-directories). Log size and retention are fixed and not configurable, and nothing rotates while a shared server runs. At each shared server startup a `<id>.log` larger than 10 MiB (10,485,760 bytes) is renamed to `<id>.log.1`, replacing any previous one, and `*.log` and `*.log.1` files in the log directory not modified for 7 days are deleted.

#### The linger depends on launch order

> [!IMPORTANT]
> A running shared server keeps, for its whole lifetime, the linger of the session proxy that started it. A later session with a different `--shared-server-linger` joins that shared server and its own value is ignored. No other setting behaves this way.

#### Falling back to a per-session server

The launch never fails because of the server mode. If, before its session is attached, the session proxy cannot compute the server identity, cannot create or verify the endpoint directory, finds the socket path over the limit, cannot start a shared server, runs out the connect wait, or loses the connection before the shared server is ready twice in a row, it writes the `Shared server unavailable, continuing as a per-session server` warning on stderr and serves the session itself as a per-session server. Nothing the host sent is lost or reordered.

#### Document refresh

Documents refresh exactly as before, as described in [Content Refresh](SPECIFICATIONS.md#content-refresh): a shared server re-checks the content on `search`, `read` and resource reads, at most once every 2 seconds; when the content changed it rebuilds the index in place, and when the resource set changed it sends `notifications/resources/list_changed`.

#### Manifest and prompt edits

An edit to the manifest or a prompt takes effect for sessions launched after it, which get a new shared server; a second shared server may run alongside the first until the sessions attached to the first close. What a running session keeps is described in [Content fixed at startup](#also-on-a-per-session-server).

#### Effects across sessions

An attached session may receive `notifications/resources/list_changed` caused by another session's request, and a rebuild briefly blocks `search` for every attached session.

#### Opting out

The one-line opt-out is `--server-mode session` (or `ACDC_MCP_SERVER_MODE=session`): every stdio launch then runs as a self-contained per-session server, as before.

#### Release

Server mode is introduced in the minor release `v0.9.0`. The version is resolved from pull request labels, and no pull request carries a major or breaking label. No existing flag, variable, config key, tool or protocol behaviour is removed or renamed. The default stdio behaviour on macOS and Linux changes to shared mode, so the commit that enables it carries a `BREAKING CHANGE:` footer. To keep the previous behaviour, see [Opting out](#opting-out).

### Known Limitations

Every limitation below comes from sharing or proxying and is avoided by [opting out](#opting-out), except those under [Also on a per-session server](#also-on-a-per-session-server), which hold in both modes and say what to do instead, and the contributor notes under [Test coverage (contributors)](#test-coverage-contributors).

#### Process lifetime

- Environments can end a detached shared server regardless of the detach: stopping a systemd unit or scope (`KillMode=control-group`; the new session does not leave the cgroup), logging out with logind `KillUserProcesses=yes`, and a container whose PID 1 exits. Without an init (`docker run --init`), an orphaned shared server lingers as a zombie. Sandboxed hosts are unverified.
- Hosts that end a whole parent-PID tree (VS Code does so after 10 seconds) also end the shared server if the session proxy is still alive at that point; the session proxy's prompt exit on stdin end of file and on signals is what avoids it. Claude Desktop and Cursor termination behaviour is unverified.
- A session proxy starts a shared server without waiting for it. On macOS and Linux a shared server that exits while the session proxy that started it is still running stays in the process table as a defunct entry until that session proxy exits. The number of such entries is bounded by the shared servers one session started.
- **Docker.** The image's default transport is stdio, so `docker run -i` is a session proxy that starts a detached shared server inside the container (endpoint directory `/tmp/acdc-mcp-0/`, log directory `/root/.local/state/acdc-mcp/`). The shared server ends with the container, so it serves more than one session only through `docker exec` into that container. The session proxy is PID 1 and does not reap, so defunct entries last for the container's life. [Opting out](#opting-out) with `-e ACDC_MCP_SERVER_MODE=session` restores one process per container.
- A hung but alive shared server is not detected.
- A session that connects while a shared server is shutting down can lose that one attempt: the shared server stops accepting first, then ends its sessions, and writes no ready line while stopping. A session admitted an instant before shutdown began is attached and then sees its connection end, which is a crash and is recovered; a connection that arrives while the shared server is stopping is dropped before the session is attached, and the session proxy's single retry applies.
- A peer that disconnects while the index is being built, or while the shared server re-checks the content for a new session, stays counted as an attached session until the next I/O on its connection, which delays the linger. A request handler or content re-check that does not stop on cancellation delays a signalled exit.

#### Identity and content edits

- Two spellings of one content directory on a case-insensitive filesystem get two shared servers.
- A whitespace-only edit to the manifest or a prompt file yields a separate shared server.
- An edit that lands between a session proxy computing the server identity and the shared server reading the content makes that one launch wait out the connect wait and continue as a per-session server.

#### Relay and recovery

- An invalid (non-JSON) host line ends the launch of a per-session server. In shared mode the shared server ends that session instead, recording the decode error in its log, and the session proxy treats it as a lost connection: it writes the crash warning and recovers onto a shared server with a new session identifier. The invalid line is not sent again. With go-sdk v1.8.0 neither mode answers a malformed line with a parse-error response.
- When host stdin ends inside an incomplete line, the session proxy exits normally (code `0`), whereas a per-session server fails with `Error: unexpected EOF` (code `1`).
- If the shared server stops reading a session's connection, the session proxy stops reading host stdin, so a host that closes stdin in that state is not noticed until the write completes; SIGINT or SIGTERM ends the session proxy at once. At stdin end of file, lines already read are still delivered, with a short bound on a blocked write.
- The server issues no requests to the host today. If server-initiated requests are added, a host response to a request issued by a lost shared server would be forwarded to its successor; that must be revisited then.
- Re-opening `subscriptions/listen` streams after a recovery relies on go-sdk accepting a replayed listen request with the same id; a permanent test guards that behaviour across go-sdk upgrades.
- How real hosts react to unsolicited `list_changed` notifications and to latency during an outage is unverified, and no automated test covers it.

#### Logging, files and credentials

- A long-lived shared server can exceed 10 MiB of log before its next startup.
- A live shared server that wrote nothing for 7 days can have its still-open log file deleted by the startup of a shared server with another identity.
- A lost lock and lock I/O errors are not logged; the session proxy sees only a failed attempt to reach a shared server.
- An existing log directory is used with whatever mode it has, a FIFO at `<id>.log` blocks the log open while the shared server holds its lock, and symbolic links among the log directory's ancestors are followed. Likewise for the endpoint directory, the replacement of an ancestor directory and setuid, setgid and sticky bits are not checked.
- Under `search.in_memory=false`, a shared server that crashes or is ended by SIGTERM or SIGINT leaves its `acdc_search_*` directory in the system temp directory, one per occurrence; nothing cleans these up, and they are left to the operating system's temp cleaner.
- Credentials passed through the environment (for example `ACDC_MCP_AUTH_*`) remain in the shared server's environment until it exits, including the linger.

#### Web UI from `browse`

- The UI that [`acdc-mcp browse`](web-ui.md#acdc-mcp-browse) starts has no authentication. Loopback is its only protection, so other local users can read the content through it while its shared server, or a foreground `browse`, runs.

#### Also on a per-session server

- `resources/list` triggers no refresh check, so its listing can lag until another call (`search`, `read`, a resource read) triggers a refresh; this is unchanged behaviour on every server. Those calls check the content at most once every 2 seconds ([Content Refresh](SPECIFICATIONS.md#content-refresh)): after that interval has elapsed since the previous check, one of them triggers a refresh, and a `resources/list` made after it reflects the refreshed catalog. A failed refresh keeps the previous catalog, and a later call retries. On a shared server, attaching a session also checks the content, without waiting for the interval, so the session's first `resources/list` reflects the content at its attach unless that refresh fails.
- Content fixed at startup is never reloaded in place: a running session keeps the `instructions`, server name, tool descriptions, index include and exclude rules and prompts it started with; only a [recovery](#recovery) moves it to the current manifest and prompts. To pick up an edit to these, start a new session.

#### Test coverage (contributors)

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

**CLI flags (per-session server under stdio, see [Opting out](#opting-out)):**
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
