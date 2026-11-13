# Transparent stdio relay over go-sdk: what a session proxy must forward

Research for [#140](https://github.com/sha1n/mcp-acdc-server/issues/140), part of the singleton-mode map [#139](https://github.com/sha1n/mcp-acdc-server/issues/139). Findings only; no decision is taken here.

This repo had no convention for research notes, so this file lives at `docs/research/` on the throwaway branch `research/stdio-relay-transport`. The branch is never merged.

## Sources

| Short name | What | Pin |
| --- | --- | --- |
| SDK | `github.com/modelcontextprotocol/go-sdk`, read from the Go module cache | `v1.8.0` (`go.mod:11`) |
| Spec legacy | MCP specification revision `2025-11-25` | <https://modelcontextprotocol.io/specification/2025-11-25> |
| Spec modern | MCP specification revision `2026-07-28` | <https://modelcontextprotocol.io/specification/2026-07-28> |
| Repo | this repository | commit `f91f657` |

Spec text was read from the `.mdx` sources in `modelcontextprotocol/modelcontextprotocol` at commit `06e9234`, directory `docs/specification/<revision>/`. SDK paths below are relative to the module root. Repo paths are relative to the repository root.

Each claim is marked:

- **[V-src]** verified by reading the cited source.
- **[V-run]** verified by running a throwaway probe against SDK v1.8.0 (see [Probe](#probe)).
- **[I]** inference drawn from verified facts; not itself tested.

## Headline facts

1. **[V-src]** SDK v1.8.0 speaks two protocol eras. `latestProtocolVersion` is `2026-07-28` (`mcp/shared.go:50-64`). The spec calls `2026-07-28` and later *modern* (per-request metadata, no handshake) and `2025-11-25` and earlier *legacy* (`initialize` handshake) ([versioning](https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning), "Terminology"). Today's stdio server already serves both: `StdioTransport` does not implement `ProtocolVersionSupporter`, so every version is advertised (`mcp/server.go:965-969`). A session proxy must therefore be transparent for both eras.
2. **[V-run]** A method-agnostic relay that pumps `jsonrpc.Message` values between two `mcp.Connection`s, without an `mcp.Client` or `mcp.Server` in the proxy, carries a full session in both eras: handshake or `server/discover`, requests, and server-to-host `list_changed` notifications.
3. **[V-run]** A legacy session can be re-established on a fresh shared server without the host re-initializing, by replaying the cached `initialize` request and `notifications/initialized` and discarding the replayed response. Without the replay the fresh server rejects every request.
4. **[V-run]** A modern session needs no replay for requests, but an open `subscriptions/listen` stream is lost on restart and the host stops receiving `list_changed`.
5. **[V-src]** This repo keeps no per-session state of its own. All session state is inside the SDK and is small: `InitializeParams`, `InitializedParams`, negotiated version, log level (`mcp/session.go:17-36`), plus modern listen subscriptions.

## 1. Client-side transports, and raw relaying

### Transports the SDK offers

**[V-src]** The transport seam is two small interfaces: `Transport.Connect(ctx) (Connection, error)` (`mcp/transport.go:52-57`) and `Connection` with `Read`, `Write`, `Close`, `SessionID` (`mcp/transport.go:73-94`). Messages are `jsonrpc.Message`, a closed set of `*jsonrpc.Request` and `*jsonrpc.Response` (`internal/jsonrpc2/messages.go:42-79`), re-exported for transport authors (`jsonrpc/jsonrpc.go:11-41`).

| Transport | Side | Wire | Cite |
| --- | --- | --- | --- |
| `StdioTransport` | either | newline-delimited JSON on stdin/stdout | `mcp/transport.go:126-138` |
| `IOTransport{Reader, Writer}` | either | newline-delimited JSON on any `io.ReadCloser` + `io.WriteCloser` | `mcp/transport.go:147-161` |
| `CommandTransport` | client | spawns a child, stdio framing | `mcp/cmd.go:18-47` |
| `InMemoryTransport` | either | `net.Pipe`, same framing | `mcp/transport.go:163-186` |
| `SSEClientTransport` | client | 2024-11-05 HTTP+SSE | `mcp/sse.go:388-484` |
| `StreamableClientTransport` | client | streamable HTTP | `mcp/streamable.go:1963-2091` |
| `LoggingTransport` | wrapper | delegates, logs each message | `mcp/transport.go:354-422` |

**[V-src]** There is no dedicated `net.Conn` transport and none is needed: a `net.Conn` satisfies both fields of `IOTransport`. **[V-run]** `IOTransport{Reader: c, Writer: c}` over a Unix domain socket works on both ends, with `Server.Connect` called once per accepted connection. One `*mcp.Server` holds many sessions (`mcp/server.go:57`, `1385-1396`).

**[V-src]** The modern spec endorses exactly this: "The wire format (one newline-delimited JSON-RPC message per line over a reliable bidirectional byte stream) works unchanged over Unix domain sockets, TCP connections, or any similar channel", and custom transports over such streams "SHOULD reuse the stdio framing" ([stdio](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/stdio); [transports, "Custom Transports"](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports)). The legacy spec permits custom transports that "preserve the JSON-RPC message format and lifecycle requirements" ([transports, "Custom Transports"](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports#custom-transports)).

**[I]** A Windows named pipe handle that implements `io.ReadWriteCloser` fits `IOTransport` the same way. Not tested.

### Can raw messages be relayed without terminating the protocol?

**[V-run]** Yes. Calling `Transport.Connect` directly yields a `Connection`; a loop of `Read` on one and `Write` on the other relays without any MCP state machine in the proxy.

**[V-src]** Terminating instead (an `mcp.Client` facing the shared server plus an `mcp.Server` facing the host) is not transparent:

- `Client.Connect` always runs its own handshake with the proxy's own `clientInfo` and capabilities (`mcp/client.go:308-403`), so the shared server never sees the host's identity or capabilities.
- A server only dispatches methods in its method table; an unknown method is rejected (`mcp/shared.go:235-239`, table at `mcp/server.go:1871-1891`). A terminating proxy has to enumerate and re-implement every method, typed.

### Which transports are usable for a raw relay

| Candidate | Finding |
| --- | --- |
| `IOTransport` over a stream socket | **[V-run]** Works for both eras, both directions. |
| `StreamableClientTransport` used raw | **[V-src]** Not suitable. The standalone SSE stream that carries unsolicited server messages is opened only from `sessionUpdated` (`mcp/streamable.go:2138-2166`), an unexported hook that only `ClientSession` calls (`mcp/client.go:332`, `401`). A raw relay never triggers it, so legacy `list_changed` notifications would not arrive. |
| `SSEClientTransport` used raw | **[V-src]** Mechanically relays (`mcp/sse.go:511-554`), but the server side `SSEServerTransport` refuses the modern era: `SupportsProtocolVersion` returns `version < 2026-07-28` (`mcp/sse.go:218-223`). A host that gets `2026-07-28` from today's stdio server would get a different `server/discover` answer through an SSE relay. No reconnect logic exists in the client connection. |
| Stateful `StreamableHTTPHandler` | **[V-src]** Serves the modern era only when configured stateless (`mcp/streamable.go:868-876`). |

**[I]** The existing `/sse` endpoint (`internal/app/server.go:50-60`) is therefore not a transparent carrier for session proxies. A stream socket with stdio framing is the only candidate found that preserves both eras.

### Fidelity limits of relaying through the SDK codec

`ioConn` decodes each line into a `Request`/`Response` and re-encodes on write (`mcp/transport.go:668-734`, `760-809`). `params`, `result` and `error.data` are `json.RawMessage` and survive intact (`internal/jsonrpc2/messages.go:54-79`, `internal/jsonrpc2/wire.go:54-71`). What does not survive:

- **[V-run]** Request ids are coerced to `int64`: `1.5` becomes `1`, and `9007199254740993` becomes `9007199254740992` (`internal/jsonrpc2/messages.go:30-40`). Fractional ids are outside the spec ("string or integer", [base protocol](https://modelcontextprotocol.io/specification/2026-07-28/basic)); integers above 2^53 are legal JSON-RPC and get corrupted.
- **[V-run]** Unknown top-level members are dropped (`"extra": true` vanished); whitespace and key order are normalized.
- **[V-src]** Batch semantics depend on a hook a raw relay never fires. `ioConn` rejects JSON-RPC batches once the negotiated version is `>= 2025-06-18`, but learns the version only via `sessionUpdated(ServerSessionState)` (`mcp/transport.go:590-601`, `701-707`). On the proxy's host-facing connection that hook never runs, so batches are accepted and re-emitted as single messages. The shared server's own `ioConn` still enforces the rule.
- **[V-src]** One inbound frame is capped at 16 MiB by default (`mcp/transport.go:122-124`, `500-504`); exceeding it is a read error that ends the connection.

**[I]** A proxy that splits on newlines and forwards the original bytes, parsing a copy only to inspect `id` and `method`, avoids every item above. The spec's framing rule makes line splitting sound: messages "MUST NOT contain embedded newlines" ([stdio](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports#stdio)).

### Request ids need no rewriting

**[V-src]** Each `Server.Connect` builds its own `jsonrpc2.Connection` (`mcp/transport.go:200-240`), so id spaces are per connection. With one proxy-to-server connection per session, host ids and server ids pass through unchanged, and so do `notifications/cancelled.requestId` and progress tokens.

## 2. What must cross the relay

A method-agnostic relay forwards all of the following by construction. The lists matter for two reasons: to show nothing needs special transport support, and to identify the few messages the proxy must *understand* (section 3).

### Legacy era (`<= 2025-11-25`)

Host to shared server:

| Message | Notes |
| --- | --- |
| `initialize` request | **[V-src]** Carries host `protocolVersion`, `capabilities`, `clientInfo` ([lifecycle](https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle#initialization)). Stored per session (`mcp/server.go:2078-2108`). Must reach the server unmodified so capability negotiation is the host's, not the proxy's. |
| `notifications/initialized` | **[V-src]** Required by spec; SDK records it and rejects duplicates (`mcp/server.go:1472-1500`). |
| `ping` | **[V-src]** Allowed before initialization (`mcp/server.go:1969-1970`); receiver "MUST respond promptly" ([ping](https://modelcontextprotocol.io/specification/2025-11-25/basic/utilities/ping)). |
| `tools/list`, `tools/call`, `resources/list`, `resources/templates/list`, `resources/read`, `prompts/list`, `prompts/get`, `completion/complete` | **[V-src]** Method table `mcp/server.go:1871-1891`. |
| `logging/setLevel` | **[V-src]** Sets per-session `LogLevel` (`mcp/server.go:2123-2129`). The capability is advertised by default because this repo leaves `ServerOptions.Capabilities` nil (`mcp/server.go:661-671`; `internal/mcp/server.go:41-44`). |
| `resources/subscribe`, `resources/unsubscribe` | **[V-src]** This server sets no `SubscribeHandler`, so the SDK answers method-not-found and does not advertise `subscribe` (`mcp/server.go:692-694`, `1216-1218`). Still relayed so the host sees the same error. |
| `notifications/cancelled` | **[V-src]** Intercepted by the SDK preempter and applied to the in-flight handler on that connection (`mcp/transport.go:252-272`). "Receivers SHOULD stop processing ... not send a response" ([cancellation](https://modelcontextprotocol.io/specification/2025-11-25/basic/utilities/cancellation)). |
| `notifications/progress`, `notifications/roots/list_changed` | **[V-src]** Accepted (`mcp/server.go:1889-1890`); this server registers no handler for either. |
| Responses to server-initiated requests | Needed only if the server issues requests; see below. |

Shared server to host:

| Message | Emitted by this server today? |
| --- | --- |
| Responses, including errors | Yes. |
| `notifications/resources/list_changed` | **[V-src]** Yes. `ResourceRegistrar.Sync` adds/removes resources (`internal/mcp/registrar.go:53-86`), which the SDK debounces 10 ms and fans out to every legacy session (`mcp/server.go:740-804`). |
| `notifications/tools/list_changed`, `notifications/prompts/list_changed` | **[V-src]** Capability advertised (`mcp/server.go:673-685`); tools and prompts are registered once at startup (`internal/mcp/server.go:49-68`), so none are sent after startup. |
| `notifications/resources/updated` | **[V-src]** No: nothing calls `Server.ResourceUpdated`. |
| `notifications/message` (logging) | **[V-src]** No: nothing calls `ServerSession.Log`; the repo logs through `slog` to stderr (`internal/app/runner.go:46-48`). |
| `notifications/progress` | **[V-src]** No: no handler calls `NotifyProgress` (`internal/mcp/tools.go`, `internal/mcp/server_helpers.go`). |
| Requests: `ping`, `roots/list`, `sampling/createMessage`, `elicitation/create` | **[V-src]** No. `KeepAlive` is unset so no pings (`mcp/server.go:1464-1466`); no handler uses `req.Session`. |

**[I]** The relay should still forward server-initiated requests and their responses, since it costs nothing in a method-agnostic design and keeps the relay correct if the server later adopts them.

### Modern era (`2026-07-28`)

**[V-src]** There is no handshake. Every request carries `io.modelcontextprotocol/protocolVersion`, `clientCapabilities`, and optional `clientInfo` in `params._meta` ([versioning](https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning); SDK detection at `mcp/shared.go:564-604`). Because `params` is relayed raw, this metadata crosses untouched.

- Host to server: `server/discover`, `subscriptions/listen`, the list/read/call methods, `notifications/cancelled`. `initialize`, `ping`, `logging/setLevel`, `resources/subscribe` are rejected by the SDK when sent with modern metadata (`mcp/server.go:1969-1977`).
- Server to host: responses (which may be `InputRequiredResult` under multi round-trip requests), `notifications/subscriptions/acknowledged`, and subscribed notifications stamped with `io.modelcontextprotocol/subscriptionId` (`mcp/server.go:806-839`, `1256-1321`).
- **[V-src]** The server must not send requests on stdio in this era: "The server MUST NOT write JSON-RPC requests to stdout" ([stdio](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/stdio)); the SDK enforces it (`mcp/server.go:1613-1628`).
- **[V-src]** `list_changed` reaches a modern session only through an active `subscriptions/listen` (`mcp/server.go:771-804`).

**[V-run]** Through the relay a modern client negotiated `2026-07-28` and the relayed sequence was `server/discover`, `subscriptions/listen`, `tools/list` up, then `notifications/subscriptions/acknowledged` and `notifications/tools/list_changed` down.

### Process-level behaviour that is not a message

**[V-src]** stdio shutdown is signalled by the host closing the server's stdin; servers "SHOULD exit promptly when their standard input is closed" ([stdio, "Shutdown"](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/stdio); legacy [lifecycle, "Shutdown"](https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle#shutdown)). stderr is free-form logging the host may capture. **[I]** The session proxy owns both: it must exit on stdin EOF and close its connection to the shared server, which makes the SDK drop that session and its subscriptions (`mcp/server.go:1400-1415`).

## 3. Re-establishing a session with a restarted shared server

### What the specification says about session identity and resumption

- **[V-src] Legacy, stdio.** The spec defines no session identifier and no resumption for stdio. Initialization "MUST be the first interaction between client and server" ([lifecycle](https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle#initialization)); the session is the connection.
- **[V-src] Legacy, HTTP.** `MCP-Session-Id` exists only for streamable HTTP. When the server no longer knows the id it answers 404 and the client "MUST start a new session by sending a new `InitializeRequest`" ([transports, "Session Management"](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports#session-management)). `Last-Event-ID` resumption replays messages of one SSE stream; it does not restore a session ([transports, "Resumability and Redelivery"](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports#resumability-and-redelivery)). Neither mechanism survives a server process restart with in-memory state.
- **[V-src] Modern.** Sessions are removed: the changelog lists "Remove protocol-level sessions and the `Mcp-Session-Id` header" ([changelog](https://modelcontextprotocol.io/specification/2026-07-28/changelog)). "Servers MUST NOT rely on prior requests over the same connection to establish context"; "an open connection, such as a STDIO process, is not a conversation or session" ([base protocol, "Statelessness"](https://modelcontextprotocol.io/specification/2026-07-28/basic)). On crash: "If the server process exits unexpectedly, the client SHOULD restart it. Because the protocol is stateless, any in-flight requests are simply lost ... Active `subscriptions/listen` streams must also be re-established after restart" ([stdio, "Unexpected Termination"](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/stdio)).

So the spec gives no legacy mechanism for what the map requires. In the legacy era the proxy has to fake continuity; in the modern era the protocol already tolerates it, except for listen streams.

### What the SDK allows

**Legacy.**

- **[V-run]** A fresh server session rejects any call before `initialize`: `tools/list` returned `{"error":{"code":0,"message":"method \"tools/list\" is invalid during session initialization"}}` (gate at `mcp/server.go:1988-1992`).
- **[V-run] Replay.** The proxy caches the host's `initialize` request and `notifications/initialized`, writes both to the new connection, and discards the response whose id matches the replayed `initialize`. The host's next `tools/list` succeeded against the new server and a later `list_changed` reached the host. The host observed nothing.
- **[V-run] Seeded state.** `Server.Connect(ctx, t, &ServerSessionOptions{State: &ServerSessionState{...}})` is public API (`mcp/server.go:1417-1441`, `mcp/session.go:17-36`). A session seeded with `InitializeParams`, `InitializedParams` and `NegotiatedProtocolVersion` served `tools/list` and delivered `list_changed` with no handshake on the wire.
- **[I]** Replay needs no change to the shared server's accept path and keeps the proxy-to-server channel pure MCP. Seeding needs the proxy to hand the state to the server out of band, that is, a non-MCP preamble on the channel. Replay looks like the lower-commitment option.
- **[V-src]** `logging/setLevel` is the only other legacy per-session value (`mcp/session.go:32-33`). **[I]** To be exact the proxy would replay the last `logging/setLevel` too and discard its response. It has no observable effect today because the server never emits log notifications.
- **[I]** The replayed `initialize` response should be compared with the one the host received (`protocolVersion`, `capabilities`, `serverInfo`, `instructions`). They can differ: capabilities are inferred from what is registered at that moment, for example `resources` appears only if at least one resource exists (`mcp/server.go:687-695`), and `instructions` come from content metadata (`internal/mcp/server.go:41-44`). The spec has no way to tell a host that these changed mid-session. What to do on mismatch is a failure-semantics decision for the map.

**Modern.**

- **[V-run]** After swapping the upstream connection with no replay, `tools/list` succeeded: per-request `_meta` re-seeds the session (`mcp/server.go:1993-1997`).
- **[V-run]** The `subscriptions/listen` stream was lost and a later tool change did not reach the host. Subscription state lives in the dead process (`mcp/server.go:60-63`, `1266-1284`).
- **[I]** Two ways to repair it, neither tested: (a) the proxy re-sends the cached `subscriptions/listen` request with the same id, which keeps the `subscriptionId` the host correlates on (`mcp/server.go:1306-1320`), and discards the second `notifications/subscriptions/acknowledged`; (b) the proxy ends the stream toward the host the way the spec lets a server end one, with a `subscriptions/listen` result or `notifications/cancelled` for that id ([subscriptions, "Cancellation"](https://modelcontextprotocol.io/specification/2026-07-28/basic/patterns/subscriptions); [cancellation](https://modelcontextprotocol.io/specification/2026-07-28/basic/patterns/cancellation)), and leaves the host to re-listen. Option (a) is invisible to the host; option (b) relies on host behaviour that was not verified.

### Both eras: what the proxy must track to make a restart invisible

- **[I] In-flight host requests.** Their responses will never arrive. The proxy must record every host request id that has no response yet and, on connection loss, answer each with a JSON-RPC error; otherwise the host waits for its own timeout. This matches the map's "apart from in-flight requests failing". The spec does not assign an error code for this; the modern spec says locally raised errors "should ensure they cannot be mistaken for errors received from the peer" ([base protocol, "Error Codes"](https://modelcontextprotocol.io/specification/2026-07-28/basic)).
- **[I] Requests arriving during the outage.** Buffer until the replay completes, or fail them. A decision for the map.
- **[I] Late `notifications/cancelled`.** A cancel for a request the proxy already failed must not be forwarded as if the new server knew that id. Receivers "MAY ignore" unknown ids, so forwarding is harmless but dropping is cleaner.
- **[I] Server-initiated requests in flight.** A host response to a request issued by the dead process must be dropped, since a new server's id counter restarts and could collide. Not reachable with today's server, which issues no requests.
- **[I] Missed change notifications.** A restarted server rebuilds its catalog from disk before any session is attached, and the SDK drops change notifications when there are no sessions (`mcp/server.go:750-757`). If content changed across the restart the host is never told. Emitting `notifications/resources/list_changed` to the host after a reconnect would close the gap; a spurious one only costs the host a re-list.

## 4. Per-session state in this server

**[V-src]** A search of `internal/` and `cmd/` (excluding tests) for `req.Session`, `ServerSession`, `Log(`, `NotifyProgress`, `Elicit`, `CreateMessage`, `ListRoots`, middleware, `KeepAlive` and `Sessions()` finds no use. Specifically:

| Location | State | Scope |
| --- | --- | --- |
| `internal/mcp/server.go:41-44` | `ServerOptions` sets only `Instructions`. No `InitializedHandler`, `KeepAlive`, `SubscribeHandler`, `CompletionHandler`, `GetSessionID`, or capability override. | Process |
| `internal/mcp/tools.go:50-87`, `internal/mcp/server_helpers.go:13-45` | Handlers read arguments and call the catalog, search service, or prompt provider. The request's `Session` and `Extra` are never read. | None |
| `internal/mcp/registrar.go:16-21`, `53-86` | `registered` map of resources. | Process; rebuilt at startup |
| `internal/app/refresh.go:29-47`, `69-141` | Debounce clock and content digests for revalidation. | Process; rebuilt at startup |
| `internal/app/runner.go:62-68` | One `Server.Run` over `StdioTransport`. | One session per process today |
| `internal/app/server.go:50-52` | SSE handler returns the same `*mcp.Server` for every request. | Already multi-session |

**[V-src]** Pagination cursors are not session-bound: a cursor is the gob-encoded id of the last item (`mcp/server.go:2179-2194`), so a cursor issued before a restart stays meaningful if the item still exists.

Consequences:

- **[V-src]** Nothing owned by this repo is lost per session when the shared server restarts. What is lost is SDK session state, all of it recoverable from what the proxy has already seen (section 3).
- **[V-src]** The server already runs many sessions on one `*mcp.Server` in SSE mode, and a catalog refresh triggered by one session's request notifies every legacy session (`internal/app/refresh.go:135-136`; `mcp/server.go:775-804`). **[I]** That is the behaviour singleton mode wants: one catalog, every host told when it changes.
- **[V-src]** `resources/list` is served by the SDK without passing through `Revalidate` (noted at `internal/app/refresh.go:131-134`). **[I]** In a shared server this staleness window is shared too, and any session's tool call refreshes the listing for all of them.

### Design smell surfaced, relevant to the map's logging note

**[V-src]** `ServerSession.ID()` comes from `Connection.SessionID()` (`mcp/server.go:1606-1611`), and `ioConn` returns `""` (`mcp/transport.go:588`). Sessions over `IOTransport` are therefore anonymous in SDK logs. A custom `Connection` wrapper can return an id, but wrapping hides `ioConn`'s unexported `sessionUpdated` hook (`mcp/transport.go:109-120`, `mcp/server.go:1574-1576`), which silently disables the batch-rejection rule described in section 1. **[I]** A per-session identifier for shared-server logs needs its own mechanism, or accepts that trade.

## Not verified

- How real MCP hosts behave: which protocol version they request, whether any probes with `server/discover` yet, and how they react to a duplicate `subscriptions/acknowledged`, a server-ended listen stream, or an unsolicited `list_changed`.
- Whether `2026-07-28` is the designated current revision. The directory exists in the spec repository beside an identical `draft`, and the SDK treats it as latest; the official status page was not checked.
- SDK behaviour on the shared-server side when a proxy connection drops with handlers still running (whether handler contexts are cancelled promptly). Not read.
- The SSE and streamable HTTP conclusions in section 1 come from reading source; no relay was run over them.
- Windows named pipes with `IOTransport`.
- The two `subscriptions/listen` repair options in section 3.
- Raw byte-forwarding (line splitting without the SDK codec) was reasoned about, not run. The probe used the SDK codec.

## Probe

A throwaway Go test, run against SDK v1.8.0 inside this module and then deleted; it is not part of the tree. It wires a real `mcp.Client` (the host) through `net.Pipe` to a proxy that pumps `jsonrpc.Message` values between an `IOTransport` connection facing the host and an `IOTransport` connection dialed to a Unix socket, where a real `mcp.Server` calls `Server.Connect` per accepted connection. "Restart" closes that socket and listener and points the proxy at a second server with a different tool.

| Case | Result |
| --- | --- |
| Legacy relay (`2025-11-25`) | Pass. Relayed: `initialize`, `notifications/initialized`, `tools/list` up; `notifications/tools/list_changed` down. |
| Modern relay (`2026-07-28`) | Pass. Relayed: `server/discover`, `subscriptions/listen`, `tools/list` up; `notifications/subscriptions/acknowledged`, `notifications/tools/list_changed` down. |
| Legacy restart, replay cached handshake, swallow response | Pass. `tools/list` returns the new server's tool; `list_changed` still reaches the host. |
| Legacy restart, `ServerSessionOptions.State` seeded | Pass. Same observations, no handshake on the wire. |
| Legacy, fresh session, no handshake | `tools/list` answered with error `method "tools/list" is invalid during session initialization` (code `0`). |
| Modern restart, no replay | `tools/list` succeeds; no `list_changed` reaches the host afterwards. |
| Codec round trip | `id: 1.5` becomes `1`; `id: 9007199254740993` becomes `9007199254740992`; unknown top-level member dropped; `params`, including `_meta` and unknown members, preserved. |
