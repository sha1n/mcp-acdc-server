# ACDC MCP Server

An MCP server that exposes a directory of authored content to agents as searchable resources and prompts.

## Language

### Singleton mode

**Shared server**:
The one process that owns the catalog and search index for a server identity, serving every session that has that identity.
_Avoid_: Primary, master, daemon, singleton

**Session proxy**:
The per-session process an MCP host launches over stdio, which relays that session to a shared server instead of owning a catalog and index itself.
_Avoid_: Client, secondary, follower

**Server identity**:
The value derived from the settings and binary version that make two launches interchangeable; sessions share a server only when their server identities are equal.
_Avoid_: Args hash, config hash, instance key
