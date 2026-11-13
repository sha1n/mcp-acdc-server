# ACDC MCP Server

An MCP server that exposes a directory of authored content to agents as searchable resources and prompts.

## Language

### Server mode

**Server mode**:
Whether a stdio launch joins a shared server or runs as a per-session server.
_Avoid_: Singleton mode

**Shared server**:
The one process that owns the catalog and search index for a server identity, serving every session that has that identity.
_Avoid_: Primary, master, daemon, singleton

**Session proxy**:
The per-session process an MCP host launches over stdio, which relays that session to a shared server instead of owning a catalog and index itself.
_Avoid_: Client, secondary, follower

**Per-session server**:
A server that owns its own catalog and search index for a single session; what a stdio launch is when the server mode is not shared, and what it becomes when a shared server cannot be used.
_Avoid_: Standalone server, legacy mode, private server

**Server identity**:
The value derived from the settings and binary version that make two launches interchangeable; sessions share a server only when their server identities are equal.
_Avoid_: Args hash, config hash, instance key

**Attached session**:
A session currently connected to a shared server.
_Avoid_: Client, connection, active session

**Linger**:
The time a shared server stays up with no attached sessions before it shuts down.
_Avoid_: Idle timeout, grace period, keep-alive
