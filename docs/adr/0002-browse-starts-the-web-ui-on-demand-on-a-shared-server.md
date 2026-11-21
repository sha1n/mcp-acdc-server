---
status: accepted
---

# `browse` starts the Web UI on demand on a shared server

People want the Web UI for content that a shared server already serves to MCP sessions. The UI settings are not part of the server identity, so the server they find may have been started without the UI. We add a `browse` command that finds or starts the shared server for its own server identity, asks it over the socket to start the Web UI if it is not running yet, opens the browser on the returned URL and exits. The UI binds `127.0.0.1` on an ephemeral port, and open viewers keep the shared server alive. `--ui-enabled` on a stdio MCP launch still has no effect, so `browse` is the only way to get the UI outside `--transport sse`.

## Considered options

- **Put `ui.enabled` in the server identity.** Launches with and without the UI would get separate shared servers for the same content, which defeats sharing and requires an identity schema bump.
- **Every shared server always serves the UI.** Opens an HTTP listener that nobody asked for.
- **Serve the UI from stdio MCP launches with `--ui-enabled`.** Ties MCP sessions to the UI. Because a UI request is required, a bind failure would fail an agent's session or its recovery, even though the agent does not need the UI.
- **Keep the shared server alive only for attached sessions.** `browse` exits right away, so the page would stop working after the linger. A separate, longer UI timeout would still break pages left idle.

## Consequences

- A request for the UI is a requirement. If `browse` cannot get a UI URL, it exits with code 1 and does not fall back to a per-session server. Under `--server-mode session` and on Windows, `browse` serves the UI itself in the foreground until it is interrupted.
- The session that starts the UI decides `ui.max_results` for the life of that shared server, as with linger.
- The UI applies no authentication. Loopback is the only protection, so other local users can read the content.
- Viewers send a heartbeat. The linger counts down only when there are no attached sessions and no viewers.
- The attach protocol gains a hello line sent by the client and a `ui_url` field in the ready line. A `browse` connection is not an attached session.
- The UI URL changes whenever its shared server is replaced.
