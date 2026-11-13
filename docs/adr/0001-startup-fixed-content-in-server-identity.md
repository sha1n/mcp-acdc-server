---
status: accepted
---

# Content fixed at startup is part of the server identity

A server reads its manifest (`.acdc/config.yaml`) and its prompt files once, at startup; only documents are covered by content refresh. With a shared server, restarting one MCP host no longer restarts the process, so an edit to the manifest or a prompt would stay invisible while any other session is attached. We put a digest of the manifest bytes and the prompt file bytes into the server identity: a launch after such an edit gets a new shared server, and running sessions keep the one they started with. This keeps the behaviour users already have ("a new launch sees the edit, a running session does not") without interrupting anyone.

## Considered options

- **Disclose the staleness only.** Removes a reload path users have today and makes the shared default a breaking change.
- **Reload the manifest and prompts in place.** `instructions` cannot change for a running session, and a mid-session change of `index.include` would be new behaviour for per-session servers too. It is a separate feature, not a property of server mode.
- **Shared server exits on a change and session proxies recover.** Fails every attached session's in-flight requests because someone edited a file, against the rule that no session is interrupted because of another.

## Consequences

- Every launch reads the manifest and walks the prompts directory before discovery.
- Two shared servers for one content directory coexist until the sessions on the older one close.
- The shared server recomputes the digest from the bytes it loaded and exits before binding if it differs from the identity it was started with, so a server is never labelled with content it does not serve.
- A session proxy recomputes the server identity at the start of every recovery. A session that survives a shared server crash therefore moves to the current manifest and prompts; this is the only case where a running session's startup-fixed content changes.
- Changing what the digest covers requires bumping the identity schema number.
